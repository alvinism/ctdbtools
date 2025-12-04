package ingest

import (
	"context"
	"fmt"
	"io"

	"ctdbtool/internal/accuraterip"
	"ctdbtool/internal/toc"
)

// ProcessPCM streams PCM reader through the AccurateRip processor based on layout.
// stride/laststride/npar control parity settings; calcParity toggles parity computation.
func ProcessPCM(ctx context.Context, r io.Reader, layout toc.Layout, stride, laststride, npar int, calcParity bool) (*accuraterip.Processor, error) {
	proc := accuraterip.NewProcessor(layout, stride, laststride, npar, calcParity)
	buf := make([]uint32, 4096)

	// iterate tracks
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

func procTailStride(p *accuraterip.Processor) int {
	if p == nil {
		return 0
	}
	// last stride already returned by parity aggregator LastStride when defaulting leadOut in StartTrack
	return 0
}
