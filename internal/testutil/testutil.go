// Package testutil provides test utilities similar to CueTools.TestHelpers.
// It includes generators for deterministic test audio data.
package testutil

import (
	"ctdbtools/internal/toc"
)

// =============================================================================
// .NET System.Random compatible implementation
// =============================================================================

// DotNetRandom implements .NET's System.Random algorithm (Knuth's subtractive RNG).
// This produces the exact same sequence as .NET Framework's Random class when
// given the same seed, enabling compatibility with CueTools test values.
//
// Reference: https://github.com/microsoft/referencesource/blob/main/mscorlib/system/random.cs
type DotNetRandom struct {
	seedArray [56]int32
	inext     int32
	inextp    int32
}

const (
	mbig  = 0x7FFFFFFF // Int32.MaxValue
	mseed = 161803398
)

// NewDotNetRandom creates a new .NET-compatible random number generator with the given seed.
func NewDotNetRandom(seed int32) *DotNetRandom {
	r := &DotNetRandom{}

	// Handle Int32.MinValue edge case
	subtraction := seed
	if seed == -0x80000000 { // Int32.MinValue
		subtraction = 0x7FFFFFFF // Int32.MaxValue
	} else if seed < 0 {
		subtraction = -seed
	}

	mj := mseed - subtraction
	r.seedArray[55] = mj
	mk := int32(1)

	for i := int32(1); i < 55; i++ {
		ii := (21 * i) % 55
		r.seedArray[ii] = mk
		mk = mj - mk
		if mk < 0 {
			mk += mbig
		}
		mj = r.seedArray[ii]
	}

	for k := 1; k < 5; k++ {
		for i := 1; i < 56; i++ {
			r.seedArray[i] -= r.seedArray[1+(i+30)%55]
			if r.seedArray[i] < 0 {
				r.seedArray[i] += mbig
			}
		}
	}

	r.inext = 0
	r.inextp = 21

	return r
}

// internalSample returns a random number in the range [0, mbig).
func (r *DotNetRandom) internalSample() int32 {
	locINext := r.inext
	locINextp := r.inextp

	locINext++
	if locINext >= 56 {
		locINext = 1
	}
	locINextp++
	if locINextp >= 56 {
		locINextp = 1
	}

	retVal := r.seedArray[locINext] - r.seedArray[locINextp]
	if retVal == mbig {
		retVal--
	}
	if retVal < 0 {
		retVal += mbig
	}

	r.seedArray[locINext] = retVal
	r.inext = locINext
	r.inextp = locINextp

	return retVal
}

// Next returns a random number in the range [0, maxValue).
func (r *DotNetRandom) Next(maxValue int32) int32 {
	return int32(float64(r.internalSample()) * (1.0 / float64(mbig)) * float64(maxValue))
}

// NextBytes fills the buffer with random bytes.
func (r *DotNetRandom) NextBytes(buffer []byte) {
	for i := range buffer {
		buffer[i] = byte(r.internalSample() % 256)
	}
}

// =============================================================================
// Noise Generator (CueTools.TestHelpers.NoiseAndErrorsGenerator compatible)
// =============================================================================

// NoiseGenerator generates deterministic pseudo-random audio samples.
// This is compatible with CueTools.TestHelpers.NoiseAndErrorsGenerator,
// using .NET's System.Random algorithm for exact value matching.
type NoiseGenerator struct {
	rnd     *DotNetRandom
	temp    []byte
	tempOff int
	offset  int
}

// NewNoiseGenerator creates a generator with the given seed and sample offset.
// The offset parameter skips that many samples in the random sequence,
// simulating drive offset behavior.
// This matches CueTools NoiseAndErrorsGenerator behavior exactly.
func NewNoiseGenerator(seed int64, offset int) *NoiseGenerator {
	rnd := NewDotNetRandom(int32(seed))
	tempSize := 8192 * 4 // 8192 samples * 4 bytes per sample (BlockAlign for RedBook)

	ng := &NoiseGenerator{
		rnd:     rnd,
		temp:    make([]byte, tempSize),
		tempOff: tempSize, // Start at end to trigger first fill
		offset:  offset,
	}

	// Skip bytes to simulate offset (offset is in samples, 4 bytes each)
	byteOff := offset * 4
	for k := 0; k < byteOff/tempSize; k++ {
		rnd.NextBytes(ng.temp)
	}
	if byteOff%tempSize > 0 {
		skipBuf := make([]byte, byteOff%tempSize)
		rnd.NextBytes(skipBuf)
	}

	return ng
}

// Next returns the next random uint32 value (for compatibility with old interface).
func (ng *NoiseGenerator) Next() uint32 {
	samples := ng.Generate(1)
	return samples[0]
}

// Generate produces n samples of deterministic pseudo-random audio.
// Each sample is a uint32 representing stereo 16-bit audio
// (low 16 bits = left, high 16 bits = right).
func (ng *NoiseGenerator) Generate(n int) []uint32 {
	samples := make([]uint32, n)
	bytesNeeded := n * 4
	buf := make([]byte, bytesNeeded)

	// Fill buffer
	bufOff := 0
	for bufOff < bytesNeeded {
		if ng.tempOff == len(ng.temp) {
			ng.rnd.NextBytes(ng.temp)
			ng.tempOff = 0
		}
		chunk := bytesNeeded - bufOff
		if chunk > len(ng.temp)-ng.tempOff {
			chunk = len(ng.temp) - ng.tempOff
		}
		copy(buf[bufOff:bufOff+chunk], ng.temp[ng.tempOff:ng.tempOff+chunk])
		bufOff += chunk
		ng.tempOff += chunk
	}

	// Convert bytes to uint32 samples (little-endian)
	for i := 0; i < n; i++ {
		samples[i] = uint32(buf[i*4]) |
			uint32(buf[i*4+1])<<8 |
			uint32(buf[i*4+2])<<16 |
			uint32(buf[i*4+3])<<24
	}

	return samples
}

// GenerateTrack produces samples for a complete track.
func (ng *NoiseGenerator) GenerateTrack(frames int) []uint32 {
	return ng.Generate(frames * 588)
}

// GenerateBytes produces n bytes of deterministic pseudo-random data.
func (ng *NoiseGenerator) GenerateBytes(n int) []byte {
	bytes := make([]byte, n)
	ng.rnd.NextBytes(bytes)
	return bytes
}

// =============================================================================
// Layout Parsing
// =============================================================================

// ParseTrackOffsets parses a space-separated string of frame offsets into a Layout.
// Format: "start1 start2 ... startN leadout"
// Example: "0 1000 2000" means track 1 starts at 0, track 2 at 1000, leadout at 2000.
func ParseTrackOffsets(offsets string) toc.Layout {
	var frames []int
	var current int
	for _, c := range offsets + " " {
		if c >= '0' && c <= '9' {
			current = current*10 + int(c-'0')
		} else if c == ' ' && current > 0 || (c == ' ' && len(frames) == 0 && current == 0) {
			frames = append(frames, current)
			current = 0
		} else if c == ' ' {
			// Skip multiple spaces
		}
	}

	if len(frames) < 2 {
		return toc.Layout{}
	}

	numTracks := len(frames) - 1
	tracks := make([]toc.Track, numTracks)
	for i := 0; i < numTracks; i++ {
		tracks[i] = toc.Track{
			Start:   frames[i],
			Length:  frames[i+1] - frames[i],
			IsAudio: true,
			Pregap:  0,
		}
	}

	return toc.Layout{
		FirstAudio:  1,
		AudioTracks: numTracks,
		Leadout:     frames[len(frames)-1],
		Tracks:      tracks,
	}
}

// =============================================================================
// Helper min/max functions
// =============================================================================

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
