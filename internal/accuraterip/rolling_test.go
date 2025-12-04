package accuraterip

import (
	"testing"

	"ctdbtool/internal/toc"
)

// tiny TOC: one audio track starting at 0 length 3 frames (3*588 samples)
func smallTOC() toc.Layout {
	return toc.Layout{
		FirstAudio:  1,
		AudioTracks: 1,
		Leadout:     3,
		Tracks: []toc.Track{
			{Start: 0, Length: 3, IsAudio: true},
		},
	}
}

func TestRollingOffsetCRCs(t *testing.T) {
	toc := smallTOC()
	rt := NewRollingTables(toc, 588, 588, false)

	// three stereo samples (represent a single frame for brevity)
	samples := []uint32{0x00010002, 0x00030004, 0x00050006}
	rt.FeedSamples(1, 0, samples)

	crc0 := rt.CRCWithOffset(1, 0, &toc)
	crcFwd := rt.CRCWithOffset(1, 1, &toc)
	crcBack := rt.CRCWithOffset(1, -1, &toc)

	if crc0 == crcFwd || crc0 == crcBack {
		t.Fatalf("offset CRCs should differ")
	}

	wn0 := rt.CRCWONULLWithOffset(1, 0, &toc)
	wnF := rt.CRCWONULLWithOffset(1, 1, &toc)
	if wn0 == wnF {
		t.Fatalf("CRCWONULL should differ when dropping non-zero sample")
	}
}
