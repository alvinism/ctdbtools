package accuraterip

import "testing"

func TestParitySyndromeRoundTrip(t *testing.T) {
	ps := NewParityState(4, 4)
	// feed a few samples
	ps.AddSamples([]uint32{0x00010002, 0x00030004}, 0)
	ps.AddSamples([]uint32{0x00050006}, 2)

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
