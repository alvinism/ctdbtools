package repair

import (
	"context"
	"fmt"
	"sort"

	"ctdbtools/internal/accuraterip"
	"ctdbtools/internal/network"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/toc"
)

// Execute performs the repair operation.
//
// This is the main entry point for repair functionality. It:
// 1. Analyzes the selected CTDB entry for errors
// 2. Calculates corrections using RS decoder + Forney algorithm
// 3. Applies corrections to the audio stream
// 4. Writes output files (WAV, CUE, log)
func Execute(
	ctx context.Context,
	proc *accuraterip.Processor,
	entry *network.CTDBEntry,
	ctdbSyndrome [][]uint16,
	layout toc.Layout,
	opts RepairOptions,
) (*RepairResult, error) {
	result := &RepairResult{
		InputPath: opts.InputPath,
		OutputDir: opts.OutputDir,
		Entry:     entry,
	}

	// Calculate layout parameters
	finalSampleCount := layout.AudioLengthFrames() * 588
	pregap := 0
	if len(layout.Tracks) > 0 {
		pregap = layout.Tracks[0].Pregap * 588
	}

	stride := entry.Stride
	laststride := stride + ((finalSampleCount-pregap)*2)%stride
	strideCount := ((finalSampleCount - pregap) * 2) / stride - 2
	pregap16bit := pregap * 2

	// Report progress: finding offset
	totalSamples := int64(finalSampleCount)
	reporter := opts.Reporter
	if reporter != nil {
		reporter.Start(totalSamples, layout.AudioTracks, opts.InputPath)
		reporter.ForceUpdate(0, 0, "Finding offset...")
	}

	// Find the correct offset (syndrome-based is fast, falls back to CRC)
	offset := findOffset(proc, entry, ctdbSyndrome, stride, laststride)
	result.Offset = offset

	// Get local syndrome at the detected offset
	localSyn := proc.Parity().SyndromeWithOffset(offset, len(ctdbSyndrome))
	if localSyn == nil {
		if reporter != nil {
			reporter.Finish()
		}
		return nil, fmt.Errorf("failed to get local syndrome at offset %d", offset)
	}

	// Report progress: calculating corrections
	if reporter != nil {
		reporter.ForceUpdate(totalSamples/10, 0, "Calculating corrections...")
	}

	// Calculate corrections using RS decoder + Forney
	rs := parity.NewRsDecode(entry.Npar)
	corrections, errorCount, err := rs.CalculateCorrections(
		localSyn, ctdbSyndrome, stride, strideCount, pregap16bit, offset)

	if err != nil {
		if reporter != nil {
			reporter.Finish()
		}
		result.CanRepair = false
		result.ErrorMessage = err.Error()
		return result, err
	}

	result.TotalErrors = errorCount
	result.Corrections = corrections

	// If no errors, nothing to repair
	if errorCount == 0 {
		if reporter != nil {
			reporter.ForceUpdate(totalSamples, 0, "No errors found")
			reporter.Finish()
		}
		result.CanRepair = true
		result.Success = true
		return result, nil
	}

	// Sort corrections by position for streaming application
	sortCorrections(corrections)
	result.Corrections = corrections

	// Calculate per-track error information
	result.TrackResults = calculateTrackResults(layout, corrections, stride, laststride, finalSampleCount, offset, pregap16bit)

	result.CanRepair = true

	// If dry-run, stop here
	if opts.DryRun {
		if reporter != nil {
			reporter.ForceUpdate(totalSamples, 0, "Dry run complete")
			reporter.Finish()
		}
		return result, nil
	}

	// Apply corrections and write output
	_, err = ApplyCorrections(ctx, opts.InputPath, opts.OutputDir, corrections, layout, opts, reporter)
	if err != nil {
		if reporter != nil {
			reporter.Finish()
		}
		result.Success = false
		result.ErrorMessage = err.Error()
		return result, err
	}

	if reporter != nil {
		reporter.ForceUpdate(totalSamples, layout.AudioTracks, "Repair complete")
		reporter.Finish()
	}

	result.Success = true
	return result, nil
}

// AnalyzeEntry analyzes a CTDB entry for errors without applying corrections.
// This is used for the candidate selection UI.
func AnalyzeEntry(
	proc *accuraterip.Processor,
	entry *network.CTDBEntry,
	ctdbSyndrome [][]uint16,
	layout toc.Layout,
	index int,
) (*RepairCandidate, error) {
	candidate := &RepairCandidate{
		Entry: entry,
		Index: index,
	}

	// Calculate layout parameters
	finalSampleCount := layout.AudioLengthFrames() * 588
	pregap := 0
	if len(layout.Tracks) > 0 {
		pregap = layout.Tracks[0].Pregap * 588
	}

	stride := entry.Stride
	laststride := stride + ((finalSampleCount-pregap)*2)%stride
	strideCount := ((finalSampleCount - pregap) * 2) / stride - 2
	pregap16bit := pregap * 2

	// Find the correct offset (syndrome-based is fast, falls back to CRC)
	offset := findOffset(proc, entry, ctdbSyndrome, stride, laststride)
	candidate.Offset = offset

	// Get local syndrome at the detected offset
	localSyn := proc.Parity().SyndromeWithOffset(offset, len(ctdbSyndrome))
	if localSyn == nil {
		candidate.CanRepair = false
		return candidate, nil
	}

	// Check for perfect match first
	xorSyn := parity.XORSyndromes(localSyn, ctdbSyndrome)
	if parity.IsZeroSyndrome(xorSyn) {
		candidate.ErrorCount = 0
		candidate.CanRepair = true
		candidate.ErrorPositions = "(no errors)"
		return candidate, nil
	}

	// Try to calculate corrections to determine if repairable
	rs := parity.NewRsDecode(entry.Npar)
	corrections, errorCount, err := rs.CalculateCorrections(
		localSyn, ctdbSyndrome, stride, strideCount, pregap16bit, offset)

	if err != nil {
		candidate.CanRepair = false
		candidate.ErrorCount = -1
		return candidate, nil
	}

	candidate.ErrorCount = errorCount
	candidate.CanRepair = true

	// Calculate per-track errors
	candidate.TrackErrors = make([]int, layout.AudioTracks)
	for track := 1; track <= layout.AudioTracks; track++ {
		trackMin, trackMax := getTrackSampleRange(layout, track)
		for _, corr := range corrections {
			if corr.Position >= trackMin && corr.Position < trackMax {
				candidate.TrackErrors[track-1]++
			}
		}
	}

	// Format error positions
	positions := make([]int, len(corrections))
	for i, c := range corrections {
		positions[i] = c.Position
	}
	candidate.ErrorPositions = parity.FormatAffectedSectors(positions)
	if candidate.ErrorPositions == "" && errorCount > 0 {
		candidate.ErrorPositions = fmt.Sprintf("(%d errors)", errorCount)
	}

	return candidate, nil
}

// findOffset searches for the matching drive offset.
// It uses syndrome-based detection first (fast), then falls back to CRC-based (slow).
//
// Offset handling in CTDB repair:
// - CTDB is offset-free: parity data represents the "correct" audio at offset 0
// - Different drives have different read offsets (typically -600 to +700 samples)
// - We detect the offset to align our local syndrome with CTDB syndrome
// - Corrections are calculated in offset-adjusted space, then applied to original positions
// - The output audio has the SAME LENGTH as input - we don't shift audio, only fix errors
func findOffset(proc *accuraterip.Processor, entry *network.CTDBEntry, ctdbSyndrome [][]uint16, stride, laststride int) int {
	// Try syndrome-based detection first (fast O(npar) per offset vs O(samples) for CRC)
	if ctdbSyndrome != nil && proc.Parity() != nil {
		if offset := findOffsetBySyndrome(proc, ctdbSyndrome, stride, entry.Npar); offset != 0 {
			return offset
		}
		// Syndrome-based found offset 0, verify it's correct using CRC
		if proc.DiscCTDBCRC(0, stride, laststride) == entry.CRC32 {
			return 0
		}
	}

	// Fall back to CRC-based detection (slow but works without syndrome data)
	return findOffsetByCRC(proc, entry.CRC32, stride, laststride)
}

// findOffsetBySyndrome searches for offset by comparing syndrome first row.
// This is much faster than CRC-based search: O(npar) per offset instead of O(samples).
// It uses fast single-row comparison (O(npar) per offset) instead of full syndrome (O(stride × npar²)).
// This mirrors CUETools CDRepair.cs FindOffset which only checks the first syndrome row.
func findOffsetBySyndrome(proc *accuraterip.Processor, ctdbSyndrome [][]uint16, stride, npar int) int {
	parityState := proc.Parity()
	if parityState == nil || ctdbSyndrome == nil || len(ctdbSyndrome) == 0 {
		return 0
	}

	strideHalf := stride / 2
	ctdbFirstRow := ctdbSyndrome[0]

	// First try offset 0 (most common case)
	localRow := parityState.SyndromeFirstRow(0)
	if localRow != nil && firstRowMatch(localRow, ctdbFirstRow, npar) {
		return 0
	}

	// Search for matching offset in range [-(stride/2)+1, (stride/2)-1]
	// CUETools: for (int offset = 1 - stride / 2; offset < stride / 2; offset++)
	for offset := 1 - strideHalf; offset < strideHalf; offset++ {
		if offset == 0 {
			continue
		}
		// CUETools uses -offset for syndrome lookup
		localRow := parityState.SyndromeFirstRow(-offset)
		if localRow == nil {
			continue
		}
		if firstRowMatch(localRow, ctdbFirstRow, npar) {
			return offset
		}
	}

	return 0
}

// firstRowMatch checks if two syndrome first rows match (XOR is all zeros).
// This is O(npar) instead of O(stride × npar) for full syndrome match.
func firstRowMatch(local, ctdb []uint16, npar int) bool {
	for j := 0; j < npar && j < len(ctdb) && j < len(local); j++ {
		if local[j]^ctdb[j] != 0 {
			return false
		}
	}
	return true
}

// syndromeMatch checks if two syndromes match (XOR is all zeros).
func syndromeMatch(local, ctdb [][]uint16, npar int) bool {
	for i := 0; i < len(ctdb) && i < len(local); i++ {
		for j := 0; j < npar && j < len(ctdb[i]) && j < len(local[i]); j++ {
			if local[i][j]^ctdb[i][j] != 0 {
				return false
			}
		}
	}
	return true
}

// findOffsetByCRC searches for offset by comparing disc CRC (slow fallback).
func findOffsetByCRC(proc *accuraterip.Processor, expectedCRC uint32, stride, laststride int) int {
	strideHalf := stride / 2

	// First try offset 0
	if proc.DiscCTDBCRC(0, stride, laststride) == expectedCRC {
		return 0
	}

	// Search for matching offset
	for offset := 1 - strideHalf; offset < strideHalf; offset++ {
		if offset == 0 {
			continue
		}
		if proc.DiscCTDBCRC(offset, stride, laststride) == expectedCRC {
			return offset
		}
	}

	return 0
}

// sortCorrections sorts corrections by position for streaming application.
func sortCorrections(corrections []parity.ErrorCorrection) {
	sort.Slice(corrections, func(i, j int) bool {
		return corrections[i].Position < corrections[j].Position
	})
}

// calculateTrackResults computes per-track repair information.
func calculateTrackResults(
	layout toc.Layout,
	corrections []parity.ErrorCorrection,
	stride, laststride, finalSampleCount, offset, pregap int,
) []TrackRepairResult {
	results := make([]TrackRepairResult, layout.AudioTracks)

	for track := 1; track <= layout.AudioTracks; track++ {
		results[track-1].Track = track

		trackMin, trackMax := getTrackSampleRange(layout, track)

		// Collect positions in this track
		var trackPositions []int
		for _, corr := range corrections {
			if corr.Position >= trackMin && corr.Position < trackMax {
				trackPositions = append(trackPositions, corr.Position)
			}
		}

		results[track-1].ErrorCount = len(trackPositions)
		results[track-1].Repaired = len(trackPositions) > 0

		if len(trackPositions) > 0 {
			results[track-1].Positions = parity.FormatAffectedSectorsFiltered(
				trackPositions, trackMin, trackMax, trackMin, 5880)
		}
	}

	return results
}

// getTrackSampleRange returns the sample range [min, max) for a track in 16-bit samples.
func getTrackSampleRange(layout toc.Layout, track int) (min, max int) {
	firstTrackStart := layout.TrackStartFrame(1)
	trackStart := layout.TrackStartFrame(track)
	trackEnd := trackStart + layout.TrackLengthFrames(track)

	// Convert frames to 16-bit samples (1 frame = 588 stereo = 1176 16-bit)
	min = (trackStart - firstTrackStart) * 588 * 2
	max = (trackEnd - firstTrackStart) * 588 * 2
	return min, max
}
