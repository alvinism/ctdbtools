package ingest

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"ctdbtools/internal/accuraterip"
	"ctdbtools/internal/progress"
	"ctdbtools/internal/toc"
)

// ProgressCallback is called during audio processing to report progress.
// Arguments: samplesProcessed, totalSamples, currentTrack, totalTracks
// Returning an error will abort processing.
type ProgressCallback func(samplesProcessed, totalSamples int64, currentTrack, totalTracks int) error

// ProcessOptions holds options for audio processing.
type ProcessOptions struct {
	Stride           int
	LastStride       int
	Npar             int
	CalcParity       bool
	Progress         *progress.Reporter // Optional progress reporter
	SeparateDecoding bool               // Use separate goroutine for decoding (like CueTools AudioPipe)
	CacheSamples     bool               // Enable sample caching for reuse (avoids second FFmpeg pass)
	SampleCache      *SampleCache       // Cache to populate during processing
}

// ProcessFile decodes an audio file via ffmpeg and feeds PCM into the AccurateRip processor.
// Caller must provide a layout (parsed from cue or external metadata).
func ProcessFile(ctx context.Context, audioPath string, layout toc.Layout, stride, laststride, npar int, calcParity bool) (*accuraterip.Processor, error) {
	return ProcessFileWithProgress(ctx, audioPath, layout, ProcessOptions{
		Stride:     stride,
		LastStride: laststride,
		Npar:       npar,
		CalcParity: calcParity,
	})
}

// sampleReader is an interface for reading samples from either direct PCM or AudioPipe.
type sampleReader interface {
	Read(buf []uint32) (int, error)
}

// directReader wraps an io.Reader for direct PCM reading.
type directReader struct {
	r io.Reader
}

func (d *directReader) Read(buf []uint32) (int, error) {
	return PCMChunk(d.r, buf)
}

// ProcessFileWithProgress decodes an audio file via ffmpeg and feeds PCM into the AccurateRip processor.
// Reports progress via the provided reporter.
// If SeparateDecoding is enabled, uses a background goroutine for buffered decoding (like CueTools AudioPipe).
func ProcessFileWithProgress(ctx context.Context, audioPath string, layout toc.Layout, opts ProcessOptions) (*accuraterip.Processor, error) {
	r, cmd, err := PCMStream(ctx, audioPath)
	if err != nil {
		return nil, err
	}
	defer cmd.Process.Kill()
	defer cmd.Wait()

	// Choose between direct reading and buffered AudioPipe
	var reader sampleReader
	if opts.SeparateDecoding {
		// Use AudioPipe for concurrent decode/process (like CueTools)
		pipe := NewAudioPipe(ctx, r, 16384) // 16K samples buffer
		pipe.Start()
		defer pipe.Close()
		reader = pipe
	} else {
		// Direct reading from FFmpeg output
		defer r.Close()
		reader = &directReader{r: r}
	}

	proc := accuraterip.NewProcessor(layout, opts.Stride, opts.LastStride, opts.Npar, opts.CalcParity)
	buf := make([]uint32, 16384)

	// Calculate total samples for progress
	totalSamples := int64(layout.AudioLengthFrames()) * 588
	var samplesProcessed int64

	// Initialize progress reporter
	if opts.Progress != nil {
		opts.Progress.Start(totalSamples, layout.AudioTracks, audioPath)
		defer opts.Progress.Finish()
	}

	for track := 1; track <= layout.AudioTracks; track++ {
		leadIn := layout.Tracks[track-1].Pregap * 588
		leadOut := procTailStride(proc)
		trackSamples := layout.TrackLengthFrames(track) * 588
		proc.StartTrack(track, leadIn, leadOut)
		remaining := trackSamples
		for remaining > 0 {
			// Check for context cancellation
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			n := min(remaining, len(buf))
			readN, err := reader.Read(buf[:n])
			if readN > 0 {
				proc.Feed(buf[:readN])
				remaining -= readN
				samplesProcessed += int64(readN)

				// Cache samples if enabled
				if opts.CacheSamples && opts.SampleCache != nil {
					opts.SampleCache.Write(buf[:readN])
				}

				// Report progress
				if opts.Progress != nil {
					status := fmt.Sprintf("Verifying track %02d...", track)
					if err := opts.Progress.Update(samplesProcessed, track, status); err != nil {
						return nil, err
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return nil, err
			}
		}
		if remaining > 0 {
			return nil, fmt.Errorf("unexpected EOF while reading track %d", track)
		}
	}
	return proc, nil
}

// ProcessCueSheet processes audio from a CUE sheet, handling both single-file and split-track cases.
// For split tracks, it uses MultiSourceReader to read from multiple files in sequence.
func ProcessCueSheet(ctx context.Context, sheet CueSheet, stride, laststride, npar int, calcParity bool) (*accuraterip.Processor, error) {
	return ProcessCueSheetWithProgress(ctx, sheet, ProcessOptions{
		Stride:     stride,
		LastStride: laststride,
		Npar:       npar,
		CalcParity: calcParity,
	})
}

// ProcessCueSheetWithProgress processes audio from a CUE sheet with progress reporting.
// For split tracks, it uses MultiSourceReader to read from multiple files in sequence.
func ProcessCueSheetWithProgress(ctx context.Context, sheet CueSheet, opts ProcessOptions) (*accuraterip.Processor, error) {
	// If not a split track CUE, use the simpler single-file processing
	if !sheet.IsSplitTrack() {
		// Get the single audio file path
		audioPath := sheet.SingleFilePath()
		if audioPath == "" && len(sheet.Sources) > 0 {
			audioPath = sheet.Sources[0].FilePath
		}
		if audioPath != "" && !filepath.IsAbs(audioPath) && sheet.CueDir != "" {
			audioPath = filepath.Join(sheet.CueDir, audioPath)
		}
		if audioPath == "" {
			return nil, fmt.Errorf("no audio file specified in CUE sheet")
		}
		return ProcessFileWithProgress(ctx, audioPath, sheet.Layout, opts)
	}

	// Split track mode: use MultiSourceReader
	reader := NewMultiSourceReader(ctx, sheet.Sources, sheet.CueDir)
	defer reader.Close()

	// Use AudioLayout for CRC calculation (has audio-only lengths)
	// while Layout is used for TOC/ID calculation
	audioLayout := sheet.GetAudioLayout()
	proc := accuraterip.NewProcessor(audioLayout, opts.Stride, opts.LastStride, opts.Npar, opts.CalcParity)
	buf := make([]uint32, 16384)

	// Calculate total samples for progress
	totalSamples := int64(audioLayout.AudioLengthFrames()) * 588
	var samplesProcessed int64

	// Initialize progress reporter
	if opts.Progress != nil {
		inputPath := ""
		if len(sheet.Sources) > 0 {
			inputPath = sheet.Sources[0].FilePath
		}
		opts.Progress.Start(totalSamples, audioLayout.AudioTracks, inputPath)
		defer opts.Progress.Finish()
	}

	for track := 1; track <= audioLayout.AudioTracks; track++ {
		leadIn := audioLayout.Tracks[track-1].Pregap * 588
		leadOut := procTailStride(proc)

		// For split tracks, use source length for reading (audio portion only)
		var trackSamples int
		if track-1 < len(sheet.Sources) && sheet.Sources[track-1].Length > 0 {
			trackSamples = int(sheet.Sources[track-1].Length)
		} else {
			trackSamples = audioLayout.TrackLengthFrames(track) * 588
		}
		proc.StartTrack(track, leadIn, leadOut)

		remaining := trackSamples
		for remaining > 0 {
			// Check for context cancellation
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			n := min(remaining, len(buf))
			readN, err := reader.Read(buf[:n])
			if readN > 0 {
				proc.Feed(buf[:readN])
				remaining -= readN
				samplesProcessed += int64(readN)

				// Cache samples if enabled
				if opts.CacheSamples && opts.SampleCache != nil {
					opts.SampleCache.Write(buf[:readN])
				}

				// Report progress
				if opts.Progress != nil {
					status := fmt.Sprintf("Verifying track %02d...", track)
					if err := opts.Progress.Update(samplesProcessed, track, status); err != nil {
						return nil, err
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return nil, err
			}
		}
		if remaining > 0 {
			return nil, fmt.Errorf("unexpected EOF while reading track %d (remaining: %d samples)", track, remaining)
		}
	}

	return proc, nil
}
