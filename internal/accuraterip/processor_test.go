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

func tocLayoutSingle() toc.Layout {
	return toc.Layout{
		FirstAudio:  1,
		AudioTracks: 1,
		Leadout:     3,
		Tracks: []toc.Track{
			{Start: 0, Length: 3, IsAudio: true},
		},
	}
}
