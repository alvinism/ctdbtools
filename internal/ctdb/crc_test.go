package ctdb

import (
	"testing"

	"ctdbtools/internal/accuraterip"
)

func TestTrackCRCOffsets(t *testing.T) {
	// track samples: A,B,C; prefix P, suffix S
	tw := accuraterip.TrackWindow{
		Prefix: []uint32{0x00070008},
		Track:  []uint32{0x00010002, 0x00030004, 0x00050006},
		Suffix: []uint32{0x0009000A},
	}
	comp := CRCComputer{MaxOffset: 4096}

	crc0, err := comp.TrackCRC(tw, 0, 0, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	crcFwd, err := comp.TrackCRC(tw, 1, 0, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	crcBack, err := comp.TrackCRC(tw, -1, 0, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if crc0 == crcFwd || crc0 == crcBack {
		t.Fatalf("expected crc to change with offset")
	}

	// trims
	crcTrim, err := comp.TrackCRC(tw, 0, 1, 1) // drop first and last track sample
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if crcTrim == crc0 {
		t.Fatalf("expected trim to change crc")
	}
}
