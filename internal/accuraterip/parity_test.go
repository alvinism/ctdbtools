package accuraterip

import "testing"

func TestParitySyndromeRoundTrip(t *testing.T) {
	ps := NewParityState(4, 4)
	// feed a few samples
	ps.AddSamples([]uint32{0x00010002, 0x00030004}, 0, 4)
	ps.AddSamples([]uint32{0x00050006}, 2, 4)

	syn := ps.Syndrome()
	if len(syn) != 4 || len(syn[0]) != 4 {
		t.Fatalf("unexpected syndrome shape")
	}
	// ensure syndromes are non-zero for non-empty parity
	allZero := true
	for _, row := range syn {
		for _, v := range row {
			if v != 0 {
				allZero = false
				break
			}
		}
	}
	if allZero {
		t.Fatalf("expected non-zero syndrome")
	}
}

func TestParityAggregator(t *testing.T) {
	agg := NewParityAggregator(4, 4, 4)
	total := 3
	agg.FeedSamples(0, []uint32{0x00010002, 0x00030004, 0x00050006}, 0, 0, total)
	syn := agg.Syndrome()
	if len(syn) != 4 || len(syn[0]) != 4 {
		t.Fatalf("unexpected syndrome shape")
	}
	found := false
	for _, row := range syn {
		for _, v := range row {
			if v != 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected aggregator syndrome to be non-zero")
	}
}

func TestSyndromeWithOffset(t *testing.T) {
	ps := NewParityState(4, 4)
	total := 4
	ps.AddSamples([]uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008}, 0, total)

	syn0 := ps.SyndromeWithOffset(0, 4)
	synOff := ps.SyndromeWithOffset(1, 4)
	if syn0 == nil || synOff == nil {
		t.Fatalf("syndromes nil")
	}
	if syndromeEqual(syn0, synOff) {
		t.Fatalf("expected offset-adjusted syndrome to differ")
	}
}

func syndromeEqual(a, b [][]uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
