package accuraterip

import "testing"

// Synthetic multi-track parity behavior: ensure lead-in/out exclusion and offset differences.
func TestParityLeadOutAndOffsets(t *testing.T) {
	layout := tocLayoutTwoTracks()
	p := NewProcessor(layout, 4, 2, 4, true)

	// Track 1: lead-in pregap=1 frame, lead-out defaults to lastStride=2 samples
	p.StartTrack(1, -1, -1)
	p.Feed([]uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008}) // last two samples should be treated as lead-out
	if !syndromeAllZero(p.Syndrome()) {
		t.Fatalf("expected parity zero when only lead-in/out fed")
	}

	// Feed actual data excluding lead-out region
	p.StartTrack(1, 0, 0)                    // explicitly ignore pregap for this test
	p.Feed([]uint32{0x0009000A, 0x000B000C}) // two samples within data region
	if syndromeAllZero(p.Syndrome()) {
		t.Fatalf("expected parity non-zero when data within window fed")
	}

	// Track 2: no pregap, lead-out defaults
	p.StartTrack(2, -1, -1)
	p.Feed([]uint32{0x000D000E, 0x000F0010, 0x00110012}) // extra sample should fall into lead-out and be skipped
	syn := p.Syndrome()
	if syn == nil || syndromeAllZero(syn) {
		t.Fatalf("expected parity on track2 data")
	}
	if p.CRC(0) == p.CRC(1) {
		t.Fatalf("expected offset CRC to differ on track2")
	}
}

func TestParityStrideMisalign(t *testing.T) {
	agg := NewParityAggregator(4, 2, 4)
	total := 6 // small window to exercise tail stride
	agg.FeedSamples(0, []uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008, 0x0009000A, 0x000B000C}, 0, 0, total)
	if syndromeAllZero(agg.Syndrome()) {
		t.Fatalf("expected main stride parity non-zero")
	}
	if tail := agg.TailSyndrome(); tail == nil || syndromeAllZero(tail) {
		t.Fatalf("expected tail stride parity non-zero")
	}
}
