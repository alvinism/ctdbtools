package ingest

import (
	"io"
	"sync"
)

// SampleCache stores decoded audio samples for reuse.
// This eliminates the need for a second FFmpeg decode pass during repair
// by caching the samples from the first pass.
type SampleCache struct {
	samples []uint32 // Contiguous backing array
	length  int64    // Number of valid samples
	mu      sync.Mutex
}

// NewSampleCache creates a new sample cache with pre-allocated capacity.
// expectedSamples is the expected number of stereo samples to store.
func NewSampleCache(expectedSamples int64) *SampleCache {
	return &SampleCache{
		samples: make([]uint32, 0, expectedSamples),
		length:  0,
	}
}

// Write appends samples to the cache.
func (c *SampleCache) Write(samples []uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.samples = append(c.samples, samples...)
	c.length = int64(len(c.samples))
	return nil
}

// Reader returns a new SampleCacheReader for sequential read access.
func (c *SampleCache) Reader() *SampleCacheReader {
	return &SampleCacheReader{
		cache:    c,
		position: 0,
	}
}

// Length returns the number of samples stored in the cache.
func (c *SampleCache) Length() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.length
}

// Samples returns the underlying sample slice for direct access.
// This should only be used after all writes are complete.
func (c *SampleCache) Samples() []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samples
}

// SampleCacheReader provides sequential read access to cached samples.
type SampleCacheReader struct {
	cache    *SampleCache
	position int64
}

// Read reads samples from the cache into buf.
// Returns the number of samples read and io.EOF when no more data.
func (r *SampleCacheReader) Read(buf []uint32) (int, error) {
	r.cache.mu.Lock()
	defer r.cache.mu.Unlock()

	if r.position >= r.cache.length {
		return 0, io.EOF
	}

	remaining := r.cache.length - r.position
	toRead := int64(len(buf))
	if toRead > remaining {
		toRead = remaining
	}

	copy(buf[:toRead], r.cache.samples[r.position:r.position+toRead])
	r.position += toRead

	if r.position >= r.cache.length {
		return int(toRead), io.EOF
	}
	return int(toRead), nil
}

// Position returns the current read position.
func (r *SampleCacheReader) Position() int64 {
	return r.position
}

// Reset resets the reader position to the beginning.
func (r *SampleCacheReader) Reset() {
	r.position = 0
}
