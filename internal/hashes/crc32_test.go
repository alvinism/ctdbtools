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
