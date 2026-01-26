package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// WAVWriter writes RedBook-quality audio to a WAV file.
// Output format: 44.1kHz, 16-bit, stereo (CD quality).
// Implements the AudioWriter interface.
type WAVWriter struct {
	file       *os.File
	dataSize   uint32
	headerSize int64
}

// Compile-time check that WAVWriter implements AudioWriter
var _ AudioWriter = (*WAVWriter)(nil)

// WAV file constants for RedBook audio
const (
	wavSampleRate    = 44100  // 44.1 kHz
	wavBitsPerSample = 16     // 16-bit
	wavChannels      = 2      // Stereo
	wavByteRate      = 176400 // 44100 * 2 * 2
	wavBlockAlign    = 4      // 2 channels * 2 bytes
)

// NewWAVWriter creates a new WAV writer for the given path.
// The file is created with a placeholder header that is finalized on Close().
func NewWAVWriter(path string) (*WAVWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAV file: %w", err)
	}

	w := &WAVWriter{
		file:       file,
		dataSize:   0,
		headerSize: 44, // Standard WAV header size
	}

	// Write placeholder header (will be updated on Close)
	if err := w.writeHeader(0); err != nil {
		file.Close()
		return nil, err
	}

	return w, nil
}

// writeHeader writes the WAV header with the specified data size.
func (w *WAVWriter) writeHeader(dataSize uint32) error {
	// Seek to beginning
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// RIFF chunk
	if _, err := w.file.Write([]byte("RIFF")); err != nil {
		return err
	}
	// File size - 8 (RIFF + size field)
	if err := binary.Write(w.file, binary.LittleEndian, dataSize+36); err != nil {
		return err
	}
	if _, err := w.file.Write([]byte("WAVE")); err != nil {
		return err
	}

	// fmt subchunk
	if _, err := w.file.Write([]byte("fmt ")); err != nil {
		return err
	}
	// Subchunk1 size (16 for PCM)
	if err := binary.Write(w.file, binary.LittleEndian, uint32(16)); err != nil {
		return err
	}
	// Audio format (1 = PCM)
	if err := binary.Write(w.file, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	// Number of channels
	if err := binary.Write(w.file, binary.LittleEndian, uint16(wavChannels)); err != nil {
		return err
	}
	// Sample rate
	if err := binary.Write(w.file, binary.LittleEndian, uint32(wavSampleRate)); err != nil {
		return err
	}
	// Byte rate
	if err := binary.Write(w.file, binary.LittleEndian, uint32(wavByteRate)); err != nil {
		return err
	}
	// Block align
	if err := binary.Write(w.file, binary.LittleEndian, uint16(wavBlockAlign)); err != nil {
		return err
	}
	// Bits per sample
	if err := binary.Write(w.file, binary.LittleEndian, uint16(wavBitsPerSample)); err != nil {
		return err
	}

	// data subchunk
	if _, err := w.file.Write([]byte("data")); err != nil {
		return err
	}
	// Data size
	if err := binary.Write(w.file, binary.LittleEndian, dataSize); err != nil {
		return err
	}

	return nil
}

// WriteSample writes a single stereo sample (32-bit packed: L16|R16) to the file.
// The sample is stored as little-endian with left channel first.
func (w *WAVWriter) WriteSample(sample uint32) error {
	// Extract left and right channels
	left := uint16(sample & 0xFFFF)
	right := uint16(sample >> 16)

	// Write left channel (little-endian)
	if err := binary.Write(w.file, binary.LittleEndian, left); err != nil {
		return err
	}
	// Write right channel (little-endian)
	if err := binary.Write(w.file, binary.LittleEndian, right); err != nil {
		return err
	}

	w.dataSize += 4
	return nil
}

// WriteSamples writes multiple stereo samples to the file.
func (w *WAVWriter) WriteSamples(samples []uint32) error {
	for _, sample := range samples {
		if err := w.WriteSample(sample); err != nil {
			return err
		}
	}
	return nil
}

// Write16BitSample writes a single 16-bit sample (one channel).
func (w *WAVWriter) Write16BitSample(sample uint16) error {
	if err := binary.Write(w.file, binary.LittleEndian, sample); err != nil {
		return err
	}
	w.dataSize += 2
	return nil
}

// WriteBytes writes raw bytes to the data section.
func (w *WAVWriter) WriteBytes(data []byte) error {
	n, err := w.file.Write(data)
	if err != nil {
		return err
	}
	w.dataSize += uint32(n)
	return nil
}

// DataSize returns the current data size in bytes.
func (w *WAVWriter) DataSize() uint32 {
	return w.dataSize
}

// SampleCount returns the number of stereo samples written.
func (w *WAVWriter) SampleCount() int64 {
	return int64(w.dataSize) / 4
}

// Close finalizes the WAV header with the actual data size and closes the file.
func (w *WAVWriter) Close() error {
	// Update header with actual data size
	if err := w.writeHeader(w.dataSize); err != nil {
		w.file.Close()
		return fmt.Errorf("failed to finalize WAV header: %w", err)
	}

	return w.file.Close()
}

// Path returns the file path.
func (w *WAVWriter) Path() string {
	return w.file.Name()
}
