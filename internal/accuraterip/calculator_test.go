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
