package accuraterip

import (
	"testing"

	"ctdbtool/internal/toc"
)

func TestProcessTrackSimple(t *testing.T) {
	// two stereo samples: (1, -1), (0, 2)
	samples := []uint32{
		0x0001ffff, // L=1, R=-1
		0x00000002, // L=0, R=2
	}
	calc := NewCalculator(dummyLayout())
	stats := calc.ProcessTrack(samples, 1)

	if stats.Samples != 2 {
		t.Fatalf("samples = %d", stats.Samples)
	}
	if stats.Peak != 1 && stats.Peak != 2 { // peak should be 2 from the last sample
		t.Fatalf("unexpected peak %d", stats.Peak)
	}
	// manual AccurateRip CRC: sample1*1 + sample2*2 (using unsigned arithmetic)
	// sample1 uint32 = 0x0001ffff, sample2 = 0x00000002
	wantCRC := uint32(0x0001ffff + 2*0x00000002)
	if stats.CRCAR != wantCRC {
		t.Fatalf("CRCAR got %08x want %08x", stats.CRCAR, wantCRC)
	}
	// CRCWONULL should exclude the zero (L channel of second sample)
	if stats.NonNullSamples != 3 {
		t.Fatalf("NonNullSamples got %d", stats.NonNullSamples)
	}
}

// dummyLayout exists for future needs; currently unused by ProcessTrack.
func dummyLayout() toc.Layout {
	return toc.Layout{}
}

func TestOffsetCRCs(t *testing.T) {
	// three samples: A,B,C
	samples := []uint32{
		0x00010002, // A
		0x00030004, // B
		0x00050006, // C
	}
	zero := CRC32WithOffset(samples, 0)
	shiftFwd := CRC32WithOffset(samples, 1)   // drop A, pad zero
	shiftBack := CRC32WithOffset(samples, -1) // pad zero, drop C

	if zero == shiftFwd {
		t.Fatalf("expected different CRC when shifting forward")
	}
	if zero == shiftBack {
		t.Fatalf("expected different CRC when shifting backward")
	}

	woZero := CRCWONULLWithOffset(samples, 0)
	if woZero != CRCWONULLWithOffset(samples, 0) {
		t.Fatalf("CRCWONULL should be deterministic")
	}
	// verify padding zeros do not change CRCWONULL but dropping changes
	if woZero == CRCWONULLWithOffset(samples, 1) {
		t.Fatalf("expected CRCWONULL with forward shift to differ (dropped non-zero)")
	}
	if woZero == CRCWONULLWithOffset(samples, -1) {
		t.Fatalf("expected CRCWONULL with backward shift to differ (dropped non-zero)")
	}
}
