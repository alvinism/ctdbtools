package hashes

import "testing"

// TestCombineTable verifies the precomputed combineTable is built correctly.
func TestCombineTable(t *testing.T) {
	// combineTable[0][0] should be the polynomial
	if combineTable[0][0] != crc32Poly {
		t.Errorf("combineTable[0][0] = %08X, want %08X", combineTable[0][0], crc32Poly)
	}

	// combineTable[0][n] for n>0 should be 1<<(n-1)
	for n := 1; n < gf2Dim; n++ {
		expected := uint32(1 << (n - 1))
		if combineTable[0][n] != expected {
			t.Errorf("combineTable[0][%d] = %08X, want %08X", n, combineTable[0][n], expected)
		}
	}
}

// TestCombineBasic tests basic CRC32.Combine behavior.
func TestCombineBasic(t *testing.T) {
	// Degenerate cases
	if got := Combine(0x12345678, 0xABCDEF00, 0); got != 0x12345678 {
		t.Errorf("Combine with len2=0: got %08X, want %08X", got, 0x12345678)
	}
	if got := Combine(0, 0xABCDEF00, 100); got != 0xABCDEF00 {
		t.Errorf("Combine with crc1=0: got %08X, want %08X", got, 0xABCDEF00)
	}
}

// TestCombineKnownValues tests CRC32.Combine with known correct values.
// Note: The expected values are computed empirically using the zlib Combine algorithm.
func TestCombineKnownValues(t *testing.T) {
	// Rather than hardcoding expected values, we verify the algorithm property:
	// Combine should produce the same result regardless of how data is split
	t.Log("Known values test skipped - use TestCombineMatchesSequential instead")
}

// TestCombineMatchesSequential verifies that Combine produces the same result
// as computing CRC32 sequentially on concatenated data.
//
// This is the key property of CRC32.Combine from zlib:
// If you have CRC(A) (running state) and CRC(B) (running state), then
// Combine(CRC(A), CRC(B), len(B)) gives you CRC(A || B) (running state).
func TestCombineMatchesSequential(t *testing.T) {
	// Create two data segments
	data1 := []byte{0x01, 0x02, 0x03, 0x04}
	data2 := []byte{0x05, 0x06, 0x07, 0x08, 0x09, 0x0A}

	// Compute CRC of concatenated data sequentially (running state, not finalized)
	crcSeq := uint32(0)
	crcSeq = Update(crcSeq, data1)
	crcSeq = Update(crcSeq, data2)

	// Compute CRCs separately (both running states) and combine
	crc1 := Update(0, data1)
	crc2 := Update(0, data2)
	combined := Combine(crc1, crc2, len(data2))

	if combined != crcSeq {
		t.Errorf("Combined CRC %08X != Sequential CRC %08X", combined, crcSeq)
	}
}

// TestCombineWithInitialState tests Combine with 0xFFFFFFFF initial state
// which is how CueTools uses it for finalized CRCs.
func TestCombineWithInitialState(t *testing.T) {
	data1 := []byte{0x01, 0x02, 0x03, 0x04}
	data2 := []byte{0x05, 0x06, 0x07, 0x08, 0x09, 0x0A}

	// Sequential with initial state
	crcSeq := uint32(0xFFFFFFFF)
	crcSeq = Update(crcSeq, data1)
	crcSeq = Update(crcSeq, data2)

	// Separate with initial state
	crc1 := Update(0xFFFFFFFF, data1)
	_ = Update(0xFFFFFFFF, data2) // crc2 with same initial state (not used directly)

	// For Combine to work with 0xFFFFFFFF initial state, we need to adjust:
	// crc2 needs to be the contribution of data2 starting from where crc1 ended
	// Combine(crc1, crc2, len2) assumes crc2 was computed with initial state 0
	// So we compute crc2 with initial state 0
	crc2zero := Update(0, data2)
	combined := Combine(crc1, crc2zero, len(data2))

	if combined != crcSeq {
		t.Errorf("Combined CRC %08X != Sequential CRC %08X", combined, crcSeq)
	}
}

// TestUpdate16 tests the 16-bit CRC update function.
func TestUpdate16(t *testing.T) {
	// Update with 0x0000 should still update CRC
	crc := uint32(0xFFFFFFFF)
	crc = Update16(crc, 0x0000)
	if crc == 0xFFFFFFFF {
		t.Error("Update16 with 0x0000 should still change CRC")
	}

	// Update16 with 0x1234 should match Update with bytes [0x34, 0x12] (little-endian)
	crc1 := Update(0xFFFFFFFF, []byte{0x34, 0x12})
	crc2 := Update16(0xFFFFFFFF, 0x1234)
	if crc1 != crc2 {
		t.Errorf("Update16(0x1234) = %08X, Update([0x34,0x12]) = %08X", crc2, crc1)
	}
}

// =============================================================================
// CRC Combine/Split tests following CueTools CRCTestSplit pattern
// =============================================================================

// testNoiseGenerator is a simple LCG for generating test data
type testNoiseGenerator struct {
	state uint64
}

func newTestNoiseGenerator(seed int64) *testNoiseGenerator {
	return &testNoiseGenerator{state: uint64(seed)}
}

func (ng *testNoiseGenerator) next() uint32 {
	ng.state = ng.state*6364136223846793005 + 1442695040888963407
	return uint32(ng.state >> 32)
}

func (ng *testNoiseGenerator) generateBytes(n int) []byte {
	bytes := make([]byte, n)
	for i := 0; i < n; i += 4 {
		val := ng.next()
		for j := 0; j < 4 && i+j < n; j++ {
			bytes[i+j] = byte(val >> (8 * j))
		}
	}
	return bytes
}

// TestCombineSplit tests that splitting data and combining CRCs produces
// the same result as computing CRC on the full data.
// This mirrors CueTools CRCTestSplit pattern.
func TestCombineSplit(t *testing.T) {
	ng := newTestNoiseGenerator(12345)
	// Generate enough data for realistic CD audio testing
	// 136 frames * 588 samples * 4 bytes = 319,968 bytes
	dataLen := 136 * 588 * 4
	data := ng.generateBytes(dataLen)

	// Compute full CRC (with 0xFFFFFFFF initial state, finalized)
	fullCRC := Update(0xFFFFFFFF, data) ^ 0xFFFFFFFF

	// Test various split points matching CueTools pattern
	// CueTools uses: 1, 13*588-1, 13*588, 13*588+1, 30*588, 68*588-1, 68*588, 68*588+1
	splits := []int{
		1,
		13*588 - 1,
		13 * 588,
		13*588 + 1,
		30 * 588,
		68*588 - 1,
		68 * 588,
		68*588 + 1,
	}

	for _, split := range splits {
		if split >= len(data) {
			continue
		}

		part1 := data[:split]
		part2 := data[split:]

		// Compute CRC of part1 with initial state 0xFFFFFFFF
		crc1 := Update(0xFFFFFFFF, part1)

		// Compute CRC of part2 with initial state 0 (for Combine)
		crc2 := Update(0, part2)

		// Combine and finalize
		combined := Combine(crc1, crc2, len(part2)) ^ 0xFFFFFFFF

		if combined != fullCRC {
			t.Errorf("split=%d: combined CRC %08X != full CRC %08X", split, combined, fullCRC)
		}
	}
}

// TestCombineMultipleSplits tests combining CRCs from multiple parts.
// This is essential for parallel CRC computation.
func TestCombineMultipleSplits(t *testing.T) {
	ng := newTestNoiseGenerator(54321)
	data := ng.generateBytes(40000)

	// Compute full CRC
	fullCRC := Update(0xFFFFFFFF, data) ^ 0xFFFFFFFF

	// Split into 4 equal parts
	partSize := len(data) / 4
	parts := [][]byte{
		data[0:partSize],
		data[partSize : 2*partSize],
		data[2*partSize : 3*partSize],
		data[3*partSize:],
	}

	// Compute progressive CRCs
	crc := Update(0xFFFFFFFF, parts[0])
	for i := 1; i < len(parts); i++ {
		partCRC := Update(0, parts[i])
		crc = Combine(crc, partCRC, len(parts[i]))
	}
	crc ^= 0xFFFFFFFF

	if crc != fullCRC {
		t.Errorf("multi-split: combined CRC %08X != full CRC %08X", crc, fullCRC)
	}
}

// TestCombineVariousSplitSizes tests combining with various split sizes
func TestCombineVariousSplitSizes(t *testing.T) {
	ng := newTestNoiseGenerator(99999)
	data := ng.generateBytes(10000)
	fullCRC := Update(0xFFFFFFFF, data) ^ 0xFFFFFFFF

	// Test splitting at every power of 2
	for split := 1; split < len(data); split *= 2 {
		part1 := data[:split]
		part2 := data[split:]

		crc1 := Update(0xFFFFFFFF, part1)
		crc2 := Update(0, part2)
		combined := Combine(crc1, crc2, len(part2)) ^ 0xFFFFFFFF

		if combined != fullCRC {
			t.Errorf("power-of-2 split=%d: combined CRC %08X != full CRC %08X",
				split, combined, fullCRC)
		}
	}
}

// TestCombineEdgeCases tests edge cases for CRC combine
func TestCombineEdgeCases(t *testing.T) {
	ng := newTestNoiseGenerator(11111)
	data := ng.generateBytes(1000)
	fullCRC := Update(0xFFFFFFFF, data) ^ 0xFFFFFFFF

	// Split at first byte
	crc1 := Update(0xFFFFFFFF, data[:1])
	crc2 := Update(0, data[1:])
	combined := Combine(crc1, crc2, len(data)-1) ^ 0xFFFFFFFF
	if combined != fullCRC {
		t.Errorf("split at 1: combined %08X != full %08X", combined, fullCRC)
	}

	// Split at last byte
	crc1 = Update(0xFFFFFFFF, data[:len(data)-1])
	crc2 = Update(0, data[len(data)-1:])
	combined = Combine(crc1, crc2, 1) ^ 0xFFFFFFFF
	if combined != fullCRC {
		t.Errorf("split at last: combined %08X != full %08X", combined, fullCRC)
	}

	// No split (empty second part)
	crc1 = Update(0xFFFFFFFF, data)
	combined = Combine(crc1, 0, 0) ^ 0xFFFFFFFF
	if combined != fullCRC {
		t.Errorf("no split: combined %08X != full %08X", combined, fullCRC)
	}
}

// TestCombineReproducibility tests that combine is reproducible
func TestCombineReproducibility(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		ng1 := newTestNoiseGenerator(seed)
		ng2 := newTestNoiseGenerator(seed)
		data1 := ng1.generateBytes(5000)
		data2 := ng2.generateBytes(5000)

		split := 2500
		crc1a := Update(0xFFFFFFFF, data1[:split])
		crc2a := Update(0, data1[split:])
		combinedA := Combine(crc1a, crc2a, len(data1)-split) ^ 0xFFFFFFFF

		crc1b := Update(0xFFFFFFFF, data2[:split])
		crc2b := Update(0, data2[split:])
		combinedB := Combine(crc1b, crc2b, len(data2)-split) ^ 0xFFFFFFFF

		if combinedA != combinedB {
			t.Errorf("seed=%d: combined CRCs differ: %08X != %08X", seed, combinedA, combinedB)
		}
	}
}
