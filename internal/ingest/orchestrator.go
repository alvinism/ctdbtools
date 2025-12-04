package ingest

import (
	"context"
	"fmt"
	"io"

	"ctdbtool/internal/accuraterip"
	"ctdbtool/internal/toc"
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
