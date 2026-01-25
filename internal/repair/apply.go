package repair

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"ctdbtools/internal/ingest"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/toc"
)

// ApplyCorrections streams audio from the input file, applies XOR corrections,
// and writes the corrected output to the output directory.
//
// The corrections are applied as the audio is streamed, avoiding the need to
// load the entire file into memory. Corrections must be sorted by position.
func ApplyCorrections(
	ctx context.Context,
	inputPath string,
	outputDir string,
	corrections []parity.ErrorCorrection,
	layout toc.Layout,
	opts RepairOptions,
) error {
	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Check if output already exists
	wavPath := filepath.Join(outputDir, "album.wav")
	if _, err := os.Stat(wavPath); err == nil && !opts.Force {
		return fmt.Errorf("output file already exists: %s (use --force to overwrite)", wavPath)
	}

	// Create WAV writer
	writer, err := NewWAVWriter(wavPath)
	if err != nil {
		return err
	}
	defer writer.Close()

	// Parse CUE to determine if split-track
	var sheet ingest.CueSheet
	if filepath.Ext(inputPath) == ".cue" {
		sheet, err = ingest.ParseCueSheetFile(inputPath)
		if err != nil {
			return fmt.Errorf("failed to parse CUE: %w", err)
		}
	} else {
		// Directory mode - discover audio files
		sheet, err = ingest.DiscoverDirectory(ctx, inputPath)
		if err != nil {
			return fmt.Errorf("failed to discover audio: %w", err)
		}
	}

	if len(sheet.Sources) == 0 {
		return fmt.Errorf("no audio files found")
	}

	// Check if split-track (multiple source files)
	if sheet.IsSplitTrack() {
		return applyCorrectionsSplitTrack(ctx, sheet, writer, corrections)
	}

	// Single file mode
	audioPath := sheet.Sources[0].FilePath
	if audioPath != "" && sheet.CueDir != "" {
		audioPath = filepath.Join(sheet.CueDir, audioPath)
	}

	return applyCorrectionsSingleFile(ctx, audioPath, writer, corrections)
}

// applyCorrectionsSingleFile handles repair for single-file CUE sheets.
func applyCorrectionsSingleFile(
	ctx context.Context,
	audioPath string,
	writer *WAVWriter,
	corrections []parity.ErrorCorrection,
) error {
	// Stream audio through FFmpeg
	stream, cmd, err := ingest.PCMStream(ctx, audioPath)
	if err != nil {
		return fmt.Errorf("failed to start audio stream: %w", err)
	}
	defer func() {
		stream.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	corrIdx := 0
	sampleIdx := 0 // Current stereo sample index

	buf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := stream.Read(buf)
		if n == 0 && err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}

		// Process each 32-bit sample (4 bytes = 2 channels × 16 bits)
		for i := 0; i+3 < n; i += 4 {
			// Read little-endian stereo sample
			left := uint16(buf[i]) | uint16(buf[i+1])<<8
			right := uint16(buf[i+2]) | uint16(buf[i+3])<<8

			// Check if left channel needs correction (16-bit sample index)
			leftIdx := sampleIdx * 2
			if corrIdx < len(corrections) && corrections[corrIdx].Position == leftIdx {
				left ^= corrections[corrIdx].Magnitude
				corrIdx++
			}

			// Check if right channel needs correction (16-bit sample index + 1)
			rightIdx := sampleIdx*2 + 1
			if corrIdx < len(corrections) && corrections[corrIdx].Position == rightIdx {
				right ^= corrections[corrIdx].Magnitude
				corrIdx++
			}

			// Write corrected sample
			sample := uint32(left) | uint32(right)<<16
			if err := writer.WriteSample(sample); err != nil {
				return fmt.Errorf("failed to write sample: %w", err)
			}

			sampleIdx++
		}
	}

	// Verify all corrections were applied
	if corrIdx < len(corrections) {
		return fmt.Errorf("not all corrections were applied: %d remaining (last applied at position %d, next needed at %d)",
			len(corrections)-corrIdx, sampleIdx*2, corrections[corrIdx].Position)
	}

	return nil
}

// applyCorrectionsSplitTrack handles repair for split track CUE sheets.
func applyCorrectionsSplitTrack(
	ctx context.Context,
	sheet ingest.CueSheet,
	writer *WAVWriter,
	corrections []parity.ErrorCorrection,
) error {
	corrIdx := 0
	globalSampleIdx := 0 // Global stereo sample counter across all tracks

	// Process each track file
	for _, source := range sheet.Sources {
		audioPath := source.FilePath
		if audioPath != "" && sheet.CueDir != "" {
			audioPath = filepath.Join(sheet.CueDir, audioPath)
		}

		// Stream this track
		stream, cmd, err := ingest.PCMStream(ctx, audioPath)
		if err != nil {
			return fmt.Errorf("failed to start stream for %s: %w", audioPath, err)
		}

		err = processStreamWithCorrections(ctx, stream, writer, corrections, &corrIdx, &globalSampleIdx)

		// Clean up
		stream.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}

		if err != nil {
			return err
		}
	}

	// Verify all corrections were applied
	if corrIdx < len(corrections) {
		return fmt.Errorf("not all corrections were applied: %d remaining (processed %d stereo samples, next correction at position %d)",
			len(corrections)-corrIdx, globalSampleIdx, corrections[corrIdx].Position)
	}

	return nil
}

// processStreamWithCorrections reads from stream, applies corrections, and writes to writer.
func processStreamWithCorrections(
	ctx context.Context,
	stream io.Reader,
	writer *WAVWriter,
	corrections []parity.ErrorCorrection,
	corrIdx *int,
	sampleIdx *int,
) error {
	buf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := stream.Read(buf)
		if n == 0 && err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}

		for i := 0; i+3 < n; i += 4 {
			left := uint16(buf[i]) | uint16(buf[i+1])<<8
			right := uint16(buf[i+2]) | uint16(buf[i+3])<<8

			// Apply corrections using global sample index
			leftIdx := (*sampleIdx) * 2
			if *corrIdx < len(corrections) && corrections[*corrIdx].Position == leftIdx {
				left ^= corrections[*corrIdx].Magnitude
				(*corrIdx)++
			}

			rightIdx := (*sampleIdx)*2 + 1
			if *corrIdx < len(corrections) && corrections[*corrIdx].Position == rightIdx {
				right ^= corrections[*corrIdx].Magnitude
				(*corrIdx)++
			}

			sample := uint32(left) | uint32(right)<<16
			if err := writer.WriteSample(sample); err != nil {
				return fmt.Errorf("failed to write sample: %w", err)
			}

			(*sampleIdx)++
		}
	}

	return nil
}

// Helper to create cleanup function
func createCleanup(stream io.ReadCloser, cmd *exec.Cmd) func() {
	return func() {
		stream.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}
}
