// Package audio provides audio I/O abstractions for CD-quality audio processing.
// This package enables future extensibility for different audio formats (WAV, FLAC)
// and metadata preservation.
package audio

// AudioWriter defines the interface for writing CD-quality audio data.
// Implementations can write to different formats (WAV, FLAC, etc.).
type AudioWriter interface {
	// WriteSample writes a single stereo sample (32-bit packed: L16|R16).
	// The sample format is little-endian with left channel in the lower 16 bits.
	WriteSample(sample uint32) error

	// WriteSamples writes multiple stereo samples to the file.
	WriteSamples(samples []uint32) error

	// Write16BitSample writes a single 16-bit sample (one channel).
	Write16BitSample(sample uint16) error

	// WriteBytes writes raw bytes to the data section.
	WriteBytes(data []byte) error

	// SampleCount returns the number of stereo samples written.
	SampleCount() int64

	// DataSize returns the current data size in bytes.
	DataSize() uint32

	// Close finalizes the file and releases resources.
	Close() error

	// Path returns the output file path.
	Path() string
}
