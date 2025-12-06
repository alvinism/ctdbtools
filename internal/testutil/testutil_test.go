package testutil

import (
	"testing"
)

// TestDotNetRandomSequence verifies that DotNetRandom produces deterministic output.
func TestDotNetRandomSequence(t *testing.T) {
	// Test with seed 2314 (used in CueTools tests)
	rnd := NewDotNetRandom(2314)

	// Generate first 10 internal samples
	vals := make([]int32, 10)
	for i := range vals {
		vals[i] = rnd.internalSample()
	}
	t.Logf("First 10 internal samples with seed 2314: %v", vals)

	// Run again with same seed - should be identical
	rnd2 := NewDotNetRandom(2314)
	for i := range vals {
		v := rnd2.internalSample()
		if v != vals[i] {
			t.Errorf("Sample %d: got %d, want %d", i, v, vals[i])
		}
	}
}

// TestDotNetRandomNextBytes verifies NextBytes produces deterministic output.
func TestDotNetRandomNextBytes(t *testing.T) {
	rnd := NewDotNetRandom(2314)
	buf := make([]byte, 20)
	rnd.NextBytes(buf)
	t.Logf("First 20 bytes with seed 2314: %v", buf)

	// Run again - should be identical
	rnd2 := NewDotNetRandom(2314)
	buf2 := make([]byte, 20)
	rnd2.NextBytes(buf2)

	for i := range buf {
		if buf[i] != buf2[i] {
			t.Errorf("Byte %d: got %d, want %d", i, buf2[i], buf[i])
		}
	}
}

// TestNoiseGeneratorSamples tests the noise generator.
func TestNoiseGeneratorSamples(t *testing.T) {
	ng := NewNoiseGenerator(2314, 0)
	samples := ng.Generate(10)
	t.Logf("First 10 samples with seed 2314: %08X %08X %08X %08X %08X %08X %08X %08X %08X %08X",
		samples[0], samples[1], samples[2], samples[3], samples[4],
		samples[5], samples[6], samples[7], samples[8], samples[9])

	// Verify reproducibility
	ng2 := NewNoiseGenerator(2314, 0)
	samples2 := ng2.Generate(10)
	for i := range samples {
		if samples[i] != samples2[i] {
			t.Errorf("Sample %d: got %08X, want %08X", i, samples2[i], samples[i])
		}
	}
}

// TestNoiseGeneratorOffset tests that offset skipping works.
func TestNoiseGeneratorOffset(t *testing.T) {
	// Generate all samples without offset
	ng0 := NewNoiseGenerator(2314, 0)
	allSamples := ng0.Generate(100)

	// Generate with offset 10 - should match samples [10:]
	ng10 := NewNoiseGenerator(2314, 10)
	offsetSamples := ng10.Generate(10)

	for i := range offsetSamples {
		if offsetSamples[i] != allSamples[10+i] {
			t.Errorf("Offset sample %d: got %08X, want %08X", i, offsetSamples[i], allSamples[10+i])
		}
	}
}
