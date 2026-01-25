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

	// Find the correct offset
	offset := findOffset(proc, entry, stride, laststride)
	result.Offset = offset

	// Get local syndrome at the detected offset
	localSyn := proc.Parity().SyndromeWithOffset(offset, len(ctdbSyndrome))
	if localSyn == nil {
		return nil, fmt.Errorf("failed to get local syndrome at offset %d", offset)
	}

	// Calculate corrections using RS decoder + Forney
	rs := parity.NewRsDecode(entry.Npar)
	corrections, errorCount, err := rs.CalculateCorrections(
		localSyn, ctdbSyndrome, stride, strideCount, pregap16bit, offset)

	if err != nil {
		result.CanRepair = false
		result.ErrorMessage = err.Error()
		return result, err
	}

	result.TotalErrors = errorCount
	result.Corrections = corrections

	// If no errors, nothing to repair
	if errorCount == 0 {
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
		return result, nil
	}

	// Apply corrections and write output
	err = ApplyCorrections(ctx, opts.InputPath, opts.OutputDir, corrections, layout, opts)
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		return result, err
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

	// Find the correct offset
	offset := findOffset(proc, entry, stride, laststride)
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

// findOffset searches for the matching drive offset by comparing disc CRC.
func findOffset(proc *accuraterip.Processor, entry *network.CTDBEntry, stride, laststride int) int {
	strideHalf := stride / 2

	// First try offset 0
	if proc.DiscCTDBCRC(0, stride, laststride) == entry.CRC32 {
		return 0
	}

	// Search for matching offset
	for offset := 1 - strideHalf; offset < strideHalf; offset++ {
		if offset == 0 {
			continue
		}
		if proc.DiscCTDBCRC(offset, stride, laststride) == entry.CRC32 {
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
