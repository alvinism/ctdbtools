package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestWAVWriter_HeaderFormat verifies the WAV header is correctly formatted.
func TestWAVWriter_HeaderFormat(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "test.wav")

	writer, err := NewWAVWriter(wavPath)
	if err != nil {
		t.Fatalf("NewWAVWriter() error: %v", err)
	}

	// Write some samples
	for i := 0; i < 1000; i++ {
		sample := uint32(i | (i << 16))
		if err := writer.WriteSample(sample); err != nil {
			t.Fatalf("WriteSample() error: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Verify header
	f, err := os.Open(wavPath)
	if err != nil {
		t.Fatalf("Failed to open output file: %v", err)
	}
	defer f.Close()

	// Read RIFF header
	var riff [4]byte
	if _, err := f.Read(riff[:]); err != nil {
		t.Fatal(err)
	}
	if string(riff[:]) != "RIFF" {
		t.Errorf("RIFF magic = %s, want RIFF", string(riff[:]))
	}

	// Read file size
	var fileSize uint32
	if err := binary.Read(f, binary.LittleEndian, &fileSize); err != nil {
		t.Fatal(err)
	}
	// fileSize = dataSize + 36
	expectedFileSize := uint32(1000*4 + 36)
	if fileSize != expectedFileSize {
		t.Errorf("File size = %d, want %d", fileSize, expectedFileSize)
	}

	// Read WAVE magic
	var wave [4]byte
	if _, err := f.Read(wave[:]); err != nil {
		t.Fatal(err)
	}
	if string(wave[:]) != "WAVE" {
		t.Errorf("WAVE magic = %s, want WAVE", string(wave[:]))
	}

	// Read fmt chunk
	var fmt [4]byte
	if _, err := f.Read(fmt[:]); err != nil {
		t.Fatal(err)
	}
	if string(fmt[:]) != "fmt " {
		t.Errorf("fmt magic = %s, want 'fmt '", string(fmt[:]))
	}

	var fmtSize uint32
	if err := binary.Read(f, binary.LittleEndian, &fmtSize); err != nil {
		t.Fatal(err)
	}
	if fmtSize != 16 {
		t.Errorf("fmt size = %d, want 16", fmtSize)
	}

	var audioFormat uint16
	if err := binary.Read(f, binary.LittleEndian, &audioFormat); err != nil {
		t.Fatal(err)
	}
	if audioFormat != 1 {
		t.Errorf("Audio format = %d, want 1 (PCM)", audioFormat)
	}

	var numChannels uint16
	if err := binary.Read(f, binary.LittleEndian, &numChannels); err != nil {
		t.Fatal(err)
	}
	if numChannels != 2 {
		t.Errorf("Num channels = %d, want 2", numChannels)
	}

	var sampleRate uint32
	if err := binary.Read(f, binary.LittleEndian, &sampleRate); err != nil {
		t.Fatal(err)
	}
	if sampleRate != 44100 {
		t.Errorf("Sample rate = %d, want 44100", sampleRate)
	}

	var byteRate uint32
	if err := binary.Read(f, binary.LittleEndian, &byteRate); err != nil {
		t.Fatal(err)
	}
	if byteRate != 176400 {
		t.Errorf("Byte rate = %d, want 176400", byteRate)
	}

	var blockAlign uint16
	if err := binary.Read(f, binary.LittleEndian, &blockAlign); err != nil {
		t.Fatal(err)
	}
	if blockAlign != 4 {
		t.Errorf("Block align = %d, want 4", blockAlign)
	}

	var bitsPerSample uint16
	if err := binary.Read(f, binary.LittleEndian, &bitsPerSample); err != nil {
		t.Fatal(err)
	}
	if bitsPerSample != 16 {
		t.Errorf("Bits per sample = %d, want 16", bitsPerSample)
	}

	// Read data chunk
	var data [4]byte
	if _, err := f.Read(data[:]); err != nil {
		t.Fatal(err)
	}
	if string(data[:]) != "data" {
		t.Errorf("data magic = %s, want 'data'", string(data[:]))
	}

	var dataSize uint32
	if err := binary.Read(f, binary.LittleEndian, &dataSize); err != nil {
		t.Fatal(err)
	}
	if dataSize != 4000 {
		t.Errorf("Data size = %d, want 4000", dataSize)
	}
}

// TestWAVWriter_SampleInterleaving verifies samples are written in correct order.
func TestWAVWriter_SampleInterleaving(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "test.wav")

	writer, err := NewWAVWriter(wavPath)
	if err != nil {
		t.Fatalf("NewWAVWriter() error: %v", err)
	}

	// Write known samples
	// Sample format: left in low 16 bits, right in high 16 bits
	samples := []uint32{
		0x00010002, // left=1, right=2
		0x00030004, // left=3, right=4
		0x00050006, // left=5, right=6
	}

	for _, s := range samples {
		if err := writer.WriteSample(s); err != nil {
			t.Fatalf("WriteSample() error: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Read back and verify
	f, err := os.Open(wavPath)
	if err != nil {
		t.Fatalf("Failed to open output file: %v", err)
	}
	defer f.Close()

	// Skip header (44 bytes)
	if _, err := f.Seek(44, 0); err != nil {
		t.Fatal(err)
	}

	// Read samples
	for i, expected := range samples {
		var left, right uint16
		if err := binary.Read(f, binary.LittleEndian, &left); err != nil {
			t.Fatalf("Read left %d: %v", i, err)
		}
		if err := binary.Read(f, binary.LittleEndian, &right); err != nil {
			t.Fatalf("Read right %d: %v", i, err)
		}

		expectedLeft := uint16(expected & 0xFFFF)
		expectedRight := uint16(expected >> 16)

		if left != expectedLeft {
			t.Errorf("Sample %d: left = %d, want %d", i, left, expectedLeft)
		}
		if right != expectedRight {
			t.Errorf("Sample %d: right = %d, want %d", i, right, expectedRight)
		}
	}
}

// TestWAVWriter_EmptyFile verifies an empty file has correct header.
func TestWAVWriter_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "empty.wav")

	writer, err := NewWAVWriter(wavPath)
	if err != nil {
		t.Fatalf("NewWAVWriter() error: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Verify file size (header only = 44 bytes)
	info, err := os.Stat(wavPath)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	if info.Size() != 44 {
		t.Errorf("File size = %d, want 44", info.Size())
	}

	if writer.DataSize() != 0 {
		t.Errorf("DataSize() = %d, want 0", writer.DataSize())
	}

	if writer.SampleCount() != 0 {
		t.Errorf("SampleCount() = %d, want 0", writer.SampleCount())
	}
}

// TestWAVWriter_SampleCount verifies sample counting.
func TestWAVWriter_SampleCount(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "count.wav")

	writer, err := NewWAVWriter(wavPath)
	if err != nil {
		t.Fatalf("NewWAVWriter() error: %v", err)
	}

	numSamples := 12345
	for i := 0; i < numSamples; i++ {
		if err := writer.WriteSample(uint32(i)); err != nil {
			t.Fatalf("WriteSample() error: %v", err)
		}
	}

	if writer.SampleCount() != int64(numSamples) {
		t.Errorf("SampleCount() = %d, want %d", writer.SampleCount(), numSamples)
	}

	if writer.DataSize() != uint32(numSamples*4) {
		t.Errorf("DataSize() = %d, want %d", writer.DataSize(), numSamples*4)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}
