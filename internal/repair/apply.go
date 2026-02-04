package repair

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ctdbtools/internal/audio"
	"ctdbtools/internal/ingest"
	"ctdbtools/internal/metadata"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/progress"
	"ctdbtools/internal/toc"
)

// ApplyCorrections streams audio from the input file, applies XOR corrections,
// and writes the corrected output to the output directory.
//
// The corrections are applied as the audio is streamed, avoiding the need to
// load the entire file into memory. Corrections must be sorted by position.
//
// If opts.SampleCache is provided and populated, uses cached samples instead of
// re-decoding with FFmpeg (significantly faster).
//
// Returns the list of output audio file paths (single file for single-file mode,
// multiple files for split-track mode).
func ApplyCorrections(
	ctx context.Context,
	inputPath string,
	outputDir string,
	corrections []parity.ErrorCorrection,
	layout toc.Layout,
	opts RepairOptions,
	reporter *progress.Reporter,
) ([]string, error) {
	// Calculate total samples for progress reporting
	totalSamples := int64(layout.AudioLengthFrames()) * 588

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Determine output format
	format := opts.Format
	if format == "" {
		format = audio.FormatWAV
	}
	ext := format.Extension()

	// Extract metadata from source files if requested
	var albumMetadata *audio.Metadata
	if opts.CopyMetadata && len(opts.SourceFiles) > 0 {
		// Resolve source file paths relative to input directory
		inputDir := inputPath
		if info, err := os.Stat(inputPath); err == nil && !info.IsDir() {
			inputDir = filepath.Dir(inputPath)
		}
		resolvedPaths := make([]string, len(opts.SourceFiles))
		for i, f := range opts.SourceFiles {
			if filepath.IsAbs(f) {
				resolvedPaths[i] = f
			} else {
				resolvedPaths[i] = filepath.Join(inputDir, f)
			}
		}

		if opts.IsSplitTrack {
			// Extract per-track metadata for split-track mode
			opts.PerTrackMetadata = metadata.ExtractFromFiles(ctx, resolvedPaths)
			// Also extract album metadata from first file for fallback
			albumMetadata = metadata.ExtractFromFirstFile(ctx, resolvedPaths)
		} else {
			albumMetadata = metadata.ExtractFromFirstFile(ctx, resolvedPaths)
		}
	}

	// Use cached samples if available (avoids second FFmpeg decode pass)
	if opts.SampleCache != nil && opts.SampleCache.Length() > 0 {
		// Split-track mode with cached samples
		if opts.IsSplitTrack {
			return applyCorrectionsCachedSplitTrack(ctx, opts.SampleCache, outputDir, corrections, layout, reporter, opts, albumMetadata)
		}

		// Single-file mode with cached samples
		audioPath := filepath.Join(outputDir, "album"+ext)
		if _, err := os.Stat(audioPath); err == nil && !opts.Force {
			return nil, fmt.Errorf("output file already exists: %s (use --force to overwrite)", audioPath)
		}

		writer, err := createWriter(audioPath, opts, albumMetadata)
		if err != nil {
			return nil, err
		}
		defer writer.Close()

		if err := applyCorrectionsCached(ctx, opts.SampleCache, writer, corrections, reporter, totalSamples); err != nil {
			return nil, err
		}
		return []string{audioPath}, nil
	}

	// Fall back to FFmpeg-based correction (original path)
	// Check if output already exists
	audioPath := filepath.Join(outputDir, "album"+ext)
	if _, err := os.Stat(audioPath); err == nil && !opts.Force {
		return nil, fmt.Errorf("output file already exists: %s (use --force to overwrite)", audioPath)
	}

	// Create writer with format support
	writer, err := createWriter(audioPath, opts, albumMetadata)
	if err != nil {
		return nil, err
	}
	defer writer.Close()

	// Parse CUE to determine if split-track
	var sheet ingest.CueSheet
	if filepath.Ext(inputPath) == ".cue" {
		sheet, err = ingest.ParseCueSheetFile(inputPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse CUE: %w", err)
		}
	} else {
		// Directory mode - discover audio files
		sheet, err = ingest.DiscoverDirectory(ctx, inputPath)
		if err != nil {
			return nil, fmt.Errorf("failed to discover audio: %w", err)
		}
	}

	if len(sheet.Sources) == 0 {
		return nil, fmt.Errorf("no audio files found")
	}

	// Check if split-track (multiple source files)
	if sheet.IsSplitTrack() {
		if err := applyCorrectionsSplitTrack(ctx, sheet, writer, corrections, reporter, totalSamples, layout.AudioTracks); err != nil {
			return nil, err
		}
		return []string{audioPath}, nil
	}

	// Single file mode
	srcAudioPath := sheet.Sources[0].FilePath
	if srcAudioPath != "" && sheet.CueDir != "" {
		srcAudioPath = filepath.Join(sheet.CueDir, srcAudioPath)
	}

	if err := applyCorrectionsSingleFile(ctx, srcAudioPath, writer, corrections, reporter, totalSamples); err != nil {
		return nil, err
	}
	return []string{audioPath}, nil
}

// createWriter creates an AudioWriter with the appropriate format and metadata.
func createWriter(path string, opts RepairOptions, meta *audio.Metadata) (audio.AudioWriter, error) {
	format := opts.Format
	if format == "" {
		format = audio.FormatWAV
	}

	writerOpts := audio.WriterOptions{
		Format:      format,
		Encoder:     opts.Encoder,
		Metadata:    meta,
		Compression: opts.Compression,
	}

	return audio.NewWriter(path, writerOpts)
}

// applyCorrectionsCached applies corrections using cached samples instead of FFmpeg.
// This is significantly faster as it avoids a second decode pass.
func applyCorrectionsCached(
	ctx context.Context,
	cache *ingest.SampleCache,
	writer audio.AudioWriter,
	corrections []parity.ErrorCorrection,
	reporter *progress.Reporter,
	totalSamples int64,
) error {
	reader := cache.Reader()
	buf := make([]uint32, 16384)

	corrIdx := 0
	sampleIdx := 0 // Current stereo sample index
	lastReportedSample := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := reader.Read(buf)
		if n == 0 && err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}

		// Apply corrections to buffer in-place
		for i := 0; i < n; i++ {
			// Check if left channel needs correction (16-bit sample index)
			leftIdx := (sampleIdx + i) * 2
			if corrIdx < len(corrections) && corrections[corrIdx].Position == leftIdx {
				// XOR left channel
				left := uint16(buf[i] & 0xFFFF)
				left ^= corrections[corrIdx].Magnitude
				buf[i] = uint32(left) | (buf[i] & 0xFFFF0000)
				corrIdx++
			}

			// Check if right channel needs correction (16-bit sample index + 1)
			rightIdx := (sampleIdx + i) * 2 + 1
			if corrIdx < len(corrections) && corrections[corrIdx].Position == rightIdx {
				// XOR right channel
				right := uint16(buf[i] >> 16)
				right ^= corrections[corrIdx].Magnitude
				buf[i] = (buf[i] & 0x0000FFFF) | uint32(right)<<16
				corrIdx++
			}
		}

		// Write entire buffer at once
		if err := writer.WriteSamples(buf[:n]); err != nil {
			return fmt.Errorf("failed to write samples: %w", err)
		}

		sampleIdx += n

		// Report progress every 100000 samples
		if reporter != nil && sampleIdx-lastReportedSample >= 100000 {
			reporter.Update(int64(sampleIdx), 0, "Applying corrections...")
			lastReportedSample = sampleIdx
		}

		if err == io.EOF {
			break
		}
	}

	// Verify all corrections were applied
	if corrIdx < len(corrections) {
		return fmt.Errorf("not all corrections were applied: %d remaining (last applied at position %d, next needed at %d)",
			len(corrections)-corrIdx, sampleIdx*2, corrections[corrIdx].Position)
	}

	return nil
}

// applyCorrectionsCachedSplitTrack applies corrections using cached samples for split-track mode.
// Creates separate audio files for each track, preserving original filenames.
func applyCorrectionsCachedSplitTrack(
	ctx context.Context,
	cache *ingest.SampleCache,
	outputDir string,
	corrections []parity.ErrorCorrection,
	layout toc.Layout,
	reporter *progress.Reporter,
	opts RepairOptions,
	albumMetadata *audio.Metadata,
) ([]string, error) {
	reader := cache.Reader()

	// Determine output format extension
	format := opts.Format
	if format == "" {
		format = audio.FormatWAV
	}
	ext := format.Extension()

	// Prepare output paths
	audioPaths := make([]string, layout.AudioTracks)
	for i := 0; i < layout.AudioTracks; i++ {
		var sourcePath string
		if i < len(opts.SourceFiles) {
			sourcePath = opts.SourceFiles[i]
		}
		audioName := deriveOutputFilenameWithFormat(sourcePath, i+1, ext)
		audioPaths[i] = filepath.Join(outputDir, audioName)

		// Check if file already exists
		if _, err := os.Stat(audioPaths[i]); err == nil && !opts.Force {
			return nil, fmt.Errorf("output file already exists: %s (use --force to overwrite)", audioPaths[i])
		}
	}

	buf := make([]uint32, 16384)
	corrIdx := 0
	globalSampleIdx := 0 // Global stereo sample index
	lastReportedSample := 0

	// Process each track
	for trackNum := 1; trackNum <= layout.AudioTracks; trackNum++ {
		// Get metadata for this specific track
		var trackMeta *audio.Metadata
		trackIdx := trackNum - 1

		if trackIdx < len(opts.PerTrackMetadata) && opts.PerTrackMetadata[trackIdx] != nil {
			// Use per-track metadata from source file
			trackMeta = opts.PerTrackMetadata[trackIdx].Clone()
		} else if albumMetadata != nil {
			// Fall back to album metadata
			trackMeta = albumMetadata.Clone()
		}

		// Always ensure track number is set
		if trackMeta != nil {
			trackMeta.Set("TRACKNUMBER", fmt.Sprintf("%d", trackNum))
			trackMeta.Set("TOTALTRACKS", fmt.Sprintf("%d", layout.AudioTracks))
		}

		// Create writer for this track
		writer, err := createWriterWithMetadata(audioPaths[trackNum-1], opts, trackMeta)
		if err != nil {
			return nil, fmt.Errorf("failed to create audio file for track %d: %w", trackNum, err)
		}

		// Calculate samples for this track
		trackLengthSamples := layout.TrackLengthFrames(trackNum) * 588
		trackEndSample := globalSampleIdx + trackLengthSamples

		if reporter != nil {
			reporter.ForceUpdate(int64(globalSampleIdx), trackNum,
				fmt.Sprintf("Writing track %d/%d...", trackNum, layout.AudioTracks))
		}

		// Read and write samples for this track
		samplesWritten := 0
		for samplesWritten < trackLengthSamples {
			select {
			case <-ctx.Done():
				writer.Close()
				return nil, ctx.Err()
			default:
			}

			// Calculate how many samples to read
			remaining := trackLengthSamples - samplesWritten
			toRead := len(buf)
			if toRead > remaining {
				toRead = remaining
			}

			n, err := reader.Read(buf[:toRead])
			if n == 0 && err != nil {
				writer.Close()
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("read error on track %d: %w", trackNum, err)
			}

			// Apply corrections to buffer in-place
			for i := 0; i < n; i++ {
				samplePos := globalSampleIdx + i
				// Check if left channel needs correction (16-bit sample index)
				leftIdx := samplePos * 2
				if corrIdx < len(corrections) && corrections[corrIdx].Position == leftIdx {
					left := uint16(buf[i] & 0xFFFF)
					left ^= corrections[corrIdx].Magnitude
					buf[i] = uint32(left) | (buf[i] & 0xFFFF0000)
					corrIdx++
				}

				// Check if right channel needs correction (16-bit sample index + 1)
				rightIdx := samplePos*2 + 1
				if corrIdx < len(corrections) && corrections[corrIdx].Position == rightIdx {
					right := uint16(buf[i] >> 16)
					right ^= corrections[corrIdx].Magnitude
					buf[i] = (buf[i] & 0x0000FFFF) | uint32(right)<<16
					corrIdx++
				}
			}

			// Write to track file
			if err := writer.WriteSamples(buf[:n]); err != nil {
				writer.Close()
				return nil, fmt.Errorf("failed to write samples for track %d: %w", trackNum, err)
			}

			globalSampleIdx += n
			samplesWritten += n

			// Report progress
			if reporter != nil && globalSampleIdx-lastReportedSample >= 100000 {
				reporter.Update(int64(globalSampleIdx), trackNum, "Applying corrections...")
				lastReportedSample = globalSampleIdx
			}

			if err == io.EOF {
				break
			}
		}

		writer.Close()

		// Verify we wrote enough samples
		if samplesWritten < trackLengthSamples && globalSampleIdx < trackEndSample {
			return nil, fmt.Errorf("incomplete track %d: wrote %d samples, expected %d", trackNum, samplesWritten, trackLengthSamples)
		}
	}

	// Verify all corrections were applied
	if corrIdx < len(corrections) {
		return nil, fmt.Errorf("not all corrections were applied: %d remaining (last applied at position %d, next needed at %d)",
			len(corrections)-corrIdx, globalSampleIdx*2, corrections[corrIdx].Position)
	}

	return audioPaths, nil
}

// createWriterWithMetadata creates an AudioWriter with specified metadata.
func createWriterWithMetadata(path string, opts RepairOptions, meta *audio.Metadata) (audio.AudioWriter, error) {
	format := opts.Format
	if format == "" {
		format = audio.FormatWAV
	}

	writerOpts := audio.WriterOptions{
		Format:      format,
		Encoder:     opts.Encoder,
		Metadata:    meta,
		Compression: opts.Compression,
	}

	return audio.NewWriter(path, writerOpts)
}

// deriveOutputFilenameWithFormat derives the output filename from original source path with specified extension.
func deriveOutputFilenameWithFormat(originalPath string, trackNum int, ext string) string {
	if originalPath == "" {
		return fmt.Sprintf("%02d%s", trackNum, ext)
	}
	base := filepath.Base(originalPath)
	origExt := filepath.Ext(base)
	return strings.TrimSuffix(base, origExt) + ext
}

// deriveOutputFilename derives the output WAV filename from original source path.
// It preserves the original filename but changes the extension to .wav.
func deriveOutputFilename(originalPath string, trackNum int) string {
	if originalPath == "" {
		return fmt.Sprintf("%02d.wav", trackNum)
	}
	base := filepath.Base(originalPath)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + ".wav"
}

// applyCorrectionsSingleFile handles repair for single-file CUE sheets.
func applyCorrectionsSingleFile(
	ctx context.Context,
	audioPath string,
	writer audio.AudioWriter,
	corrections []parity.ErrorCorrection,
	reporter *progress.Reporter,
	totalSamples int64,
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
	lastReportedSample := 0

	buf := make([]byte, 16384)

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

			// Report progress every 10000 samples
			if reporter != nil && sampleIdx-lastReportedSample >= 10000 {
				reporter.Update(int64(sampleIdx), 0, "Applying corrections...")
				lastReportedSample = sampleIdx
			}
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
	writer audio.AudioWriter,
	corrections []parity.ErrorCorrection,
	reporter *progress.Reporter,
	totalSamples int64,
	totalTracks int,
) error {
	corrIdx := 0
	globalSampleIdx := 0 // Global stereo sample counter across all tracks
	lastReportedSample := 0

	// Process each track file
	for trackNum, source := range sheet.Sources {
		audioPath := source.FilePath
		if audioPath != "" && sheet.CueDir != "" {
			audioPath = filepath.Join(sheet.CueDir, audioPath)
		}

		// Report progress for this track
		if reporter != nil {
			reporter.ForceUpdate(int64(globalSampleIdx), trackNum+1,
				fmt.Sprintf("Applying corrections to track %d/%d...", trackNum+1, totalTracks))
		}

		// Stream this track
		stream, cmd, err := ingest.PCMStream(ctx, audioPath)
		if err != nil {
			return fmt.Errorf("failed to start stream for %s: %w", audioPath, err)
		}

		err = processStreamWithCorrections(ctx, stream, writer, corrections, &corrIdx, &globalSampleIdx, reporter, &lastReportedSample, trackNum+1)

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
	writer audio.AudioWriter,
	corrections []parity.ErrorCorrection,
	corrIdx *int,
	sampleIdx *int,
	reporter *progress.Reporter,
	lastReportedSample *int,
	currentTrack int,
) error {
	buf := make([]byte, 16384)

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

			// Report progress every 10000 samples
			if reporter != nil && *sampleIdx-*lastReportedSample >= 10000 {
				reporter.Update(int64(*sampleIdx), currentTrack, "Applying corrections...")
				*lastReportedSample = *sampleIdx
			}
		}
	}

	return nil
}

