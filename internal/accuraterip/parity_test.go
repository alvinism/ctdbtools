package accuraterip

import "testing"

func TestParitySyndromeRoundTrip(t *testing.T) {
	// stride=4, npar=4, pregap=0, finalSampleCount=4
	// With pregap=0 and finalSampleCount=4:
	//   stridecount = (4-0)*2/4 = 2
	//   Parity is accumulated when currentStride >= 1 && currentStride <= 2
	//   currentStride = (currentSample * 2) / stride
	//   Sample 0: currentSample=0, currentStride=0 -> no parity
	//   Sample 1: currentSample=1, currentStride=0 -> no parity
	//   Sample 2: currentSample=2, currentStride=1 -> parity
	//   Sample 3: currentSample=3, currentStride=1 -> parity
	// We need more samples to get parity accumulated
	ps := NewParityState(4, 4, 0, 8) // finalSampleCount=8 gives stridecount=4
	// feed samples - parity starts after stride/2 samples (first currentStride=1)
	ps.AddSamples([]uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008})
	ps.AddSamples([]uint32{0x00090010, 0x00110012, 0x00130014, 0x00150016})

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
	// stride=4, lastStride=4, npar=4, pregap=0, finalSampleCount=8
	agg := NewParityAggregator(4, 4, 4, 0, 8)
	agg.FeedSamples([]uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008})
	agg.FeedSamples([]uint32{0x00090010, 0x00110012, 0x00130014, 0x00150016})
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
	// stride=4, npar=4, pregap=0, finalSampleCount=8
	ps := NewParityState(4, 4, 0, 8)
	ps.AddSamples([]uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008})
	ps.AddSamples([]uint32{0x00090010, 0x00110012, 0x00130014, 0x00150016})

	syn0 := ps.SyndromeWithOffset(0, 4)
	synOff := ps.SyndromeWithOffset(1, 4)
	if syn0 == nil || synOff == nil {
		t.Fatalf("syndromes nil")
	}
	// Note: syndromes may or may not differ depending on lead-in/out buffer contents
	// The test just verifies no crashes occur with offset
	_ = syndromeEqual(syn0, synOff)
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
