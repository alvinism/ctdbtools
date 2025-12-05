package ingest

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"ctdbtools/internal/accuraterip"
	"ctdbtools/internal/toc"
)

// ProcessFile decodes an audio file via ffmpeg and feeds PCM into the AccurateRip processor.
// Caller must provide a layout (parsed from cue or external metadata).
func ProcessFile(ctx context.Context, audioPath string, layout toc.Layout, stride, laststride, npar int, calcParity bool) (*accuraterip.Processor, error) {
	r, cmd, err := PCMStream(ctx, audioPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	defer cmd.Process.Kill()
	defer cmd.Wait()

	proc := accuraterip.NewProcessor(layout, stride, laststride, npar, calcParity)
	buf := make([]uint32, 4096)
	for track := 1; track <= layout.AudioTracks; track++ {
		leadIn := layout.Tracks[track-1].Pregap * 588
		leadOut := procTailStride(proc)
		totalSamples := layout.TrackLengthFrames(track) * 588
		proc.StartTrack(track, leadIn, leadOut)
		remaining := totalSamples
		for remaining > 0 {
			n := remaining
			if n > len(buf) {
				n = len(buf)
			}
			readN, err := PCMChunk(r, buf[:n])
			if readN > 0 {
				proc.Feed(buf[:readN])
				remaining -= readN
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
		return ProcessFile(ctx, audioPath, sheet.Layout, stride, laststride, npar, calcParity)
	}

	// Split track mode: use MultiSourceReader
	reader := NewMultiSourceReader(ctx, sheet.Sources, sheet.CueDir)
	defer reader.Close()

	// Use AudioLayout for CRC calculation (has audio-only lengths)
	// while Layout is used for TOC/ID calculation
	audioLayout := sheet.GetAudioLayout()
	proc := accuraterip.NewProcessor(audioLayout, stride, laststride, npar, calcParity)
	buf := make([]uint32, 4096)

	for track := 1; track <= audioLayout.AudioTracks; track++ {
		leadIn := audioLayout.Tracks[track-1].Pregap * 588
		leadOut := procTailStride(proc)

		// For split tracks, use source length for reading (audio portion only)
		var totalSamples int
		if track-1 < len(sheet.Sources) && sheet.Sources[track-1].Length > 0 {
			totalSamples = int(sheet.Sources[track-1].Length)
		} else {
			totalSamples = audioLayout.TrackLengthFrames(track) * 588
		}
		proc.StartTrack(track, leadIn, leadOut)

		remaining := totalSamples
		for remaining > 0 {
			n := remaining
			if n > len(buf) {
				n = len(buf)
			}
			readN, err := reader.Read(buf[:n])
			if readN > 0 {
				proc.Feed(buf[:readN])
				remaining -= readN
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
