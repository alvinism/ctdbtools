package accuraterip

import (
	"testing"

	"ctdbtool/internal/toc"
)

func TestProcessorCRCFlow(t *testing.T) {
	layout := tocLayoutSingle()
	p := NewProcessor(layout, 4, 4, 4, true)
	p.StartTrack(1, 0, 0)
	p.Feed([]uint32{0x00010002, 0x00030004, 0x00050006})

	crc0 := p.CRC(0)
	crcF := p.CRC(1)
	if crc0 == crcF {
		t.Fatalf("expected crc to change with offset")
	}
	if p.Syndrome() == nil {
		t.Fatalf("expected syndrome")
	}
}

func TestProcessorLeadInSkipParity(t *testing.T) {
	layout := tocLayoutSingle()
	p := NewProcessor(layout, 4, 2, 4, true)
	// auto derive lead-in from Pregap (1 frame -> 588 samples), lead-out defaults to lastStride (2)
	p.StartTrack(1, -1, -1)
	p.Feed([]uint32{0x00000000, 0x00010002}) // first sample within lead-in should be skipped
	syn := p.Syndrome()
	if !syndromeAllZero(syn) {
		t.Fatalf("expected parity to skip lead-in and remain zero")
	}
	if tail := p.TailSyndrome(); tail == nil {
		t.Fatalf("expected tail syndrome when lastStride differs")
	} else if !syndromeAllZero(tail) {
		t.Fatalf("expected tail parity to be zero as well")
	}

	// feed data excluding lead-out (totalSamples=3*588, leadOut=2 -> last 2 samples skipped)
	p.StartTrack(1, -1, -1)
	p.Feed([]uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008})
}

func tocLayoutSingle() toc.Layout {
	return toc.Layout{
		FirstAudio:  1,
		AudioTracks: 1,
		Leadout:     3,
		Tracks: []toc.Track{
			{Start: 0, Length: 3, IsAudio: true, Pregap: 1},
		},
	}
}

func syndromeAllZero(s [][]uint16) bool {
	if s == nil {
		return true
	}
	for _, row := range s {
		for _, v := range row {
			if v != 0 {
				return false
			}
		}
	}
	return true
}
