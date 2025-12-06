package ingest

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// mockPCMReader simulates PCM audio data for testing AudioPipe.
type mockPCMReader struct {
	data   []byte
	pos    int
	closed bool
}

func newMockPCMReader(samples []uint32) *mockPCMReader {
	// Convert samples to bytes (little-endian stereo 16-bit)
	data := make([]byte, len(samples)*4)
	for i, s := range samples {
		// Low 16 bits = left channel, high 16 bits = right channel
		lo := uint16(s & 0xFFFF)
		hi := uint16((s >> 16) & 0xFFFF)
		data[i*4+0] = byte(lo)
		data[i*4+1] = byte(lo >> 8)
		data[i*4+2] = byte(hi)
		data[i*4+3] = byte(hi >> 8)
	}
	return &mockPCMReader{data: data}
}

func (m *mockPCMReader) Read(p []byte) (int, error) {
	if m.pos >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockPCMReader) Close() error {
	m.closed = true
	return nil
}

func TestAudioPipeBasic(t *testing.T) {
	// Create test samples
	samples := []uint32{0x00010002, 0x00030004, 0x00050006, 0x00070008}
	reader := newMockPCMReader(samples)

	ctx := context.Background()
	pipe := NewAudioPipe(ctx, reader, 1024)
	pipe.Start()

	// Read all samples
	buf := make([]uint32, 4)
	n, err := pipe.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 4 {
		t.Fatalf("Expected 4 samples, got %d", n)
	}

	// Verify samples
	for i, expected := range samples {
		if buf[i] != expected {
			t.Errorf("Sample %d: expected %08X, got %08X", i, expected, buf[i])
		}
	}

	pipe.Close()

	if !reader.closed {
		t.Error("Reader should be closed after pipe.Close()")
	}
}

func TestAudioPipePartialReads(t *testing.T) {
	// Create more test samples
	samples := make([]uint32, 100)
	for i := range samples {
		samples[i] = uint32(i)
	}
	reader := newMockPCMReader(samples)

	ctx := context.Background()
	pipe := NewAudioPipe(ctx, reader, 1024)
	pipe.Start()

	// Read in small chunks
	var all []uint32
	buf := make([]uint32, 7) // Odd number to test partial reads
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			all = append(all, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
	}

	if len(all) != len(samples) {
		t.Fatalf("Expected %d samples, got %d", len(samples), len(all))
	}

	for i, expected := range samples {
		if all[i] != expected {
			t.Errorf("Sample %d: expected %08X, got %08X", i, expected, all[i])
		}
	}

	pipe.Close()
}

func TestAudioPipeContextCancel(t *testing.T) {
	// Create a reader that would block forever
	samples := make([]uint32, 10000)
	reader := newMockPCMReader(samples)

	ctx, cancel := context.WithCancel(context.Background())
	pipe := NewAudioPipe(ctx, reader, 16)
	pipe.Start()

	// Read some data
	buf := make([]uint32, 10)
	_, err := pipe.Read(buf)
	if err != nil {
		t.Fatalf("First read failed: %v", err)
	}

	// Cancel context
	cancel()

	// Close should complete without hanging
	done := make(chan struct{})
	go func() {
		pipe.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good, Close completed
	case <-context.Background().Done():
		t.Fatal("Close timed out after context cancel")
	}
}

func TestAudioPipeEmptyRead(t *testing.T) {
	// Empty reader
	reader := &mockPCMReader{data: []byte{}}

	ctx := context.Background()
	pipe := NewAudioPipe(ctx, reader, 1024)
	pipe.Start()

	buf := make([]uint32, 10)
	n, err := pipe.Read(buf)

	if n != 0 {
		t.Errorf("Expected 0 samples from empty reader, got %d", n)
	}
	if err != io.EOF {
		t.Errorf("Expected EOF from empty reader, got %v", err)
	}

	pipe.Close()
}

func TestAudioPipeLargeData(t *testing.T) {
	// Create 1MB of sample data (262144 samples)
	numSamples := 262144
	samples := make([]uint32, numSamples)
	for i := range samples {
		samples[i] = uint32(i * 0x10001) // Pattern that's easy to verify
	}
	reader := newMockPCMReader(samples)

	ctx := context.Background()
	pipe := NewAudioPipe(ctx, reader, 16384)
	pipe.Start()

	// Read all data
	var all []uint32
	buf := make([]uint32, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			all = append(all, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
	}

	if len(all) != numSamples {
		t.Fatalf("Expected %d samples, got %d", numSamples, len(all))
	}

	// Verify first, middle, and last samples
	checkpoints := []int{0, numSamples / 2, numSamples - 1}
	for _, i := range checkpoints {
		expected := uint32(i * 0x10001)
		if all[i] != expected {
			t.Errorf("Sample %d: expected %08X, got %08X", i, expected, all[i])
		}
	}

	pipe.Close()
}

// TestAudioPipeMatchesDirectRead verifies that AudioPipe produces
// the same results as direct PCMChunk reading.
func TestAudioPipeMatchesDirectRead(t *testing.T) {
	samples := make([]uint32, 1000)
	for i := range samples {
		samples[i] = uint32(i * 0x12345)
	}

	// Read directly using PCMChunk
	reader1 := newMockPCMReader(samples)
	var direct []uint32
	buf := make([]uint32, 100)
	for {
		n, err := PCMChunk(reader1, buf)
		if n > 0 {
			direct = append(direct, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Direct read failed: %v", err)
		}
	}

	// Read via AudioPipe
	reader2 := newMockPCMReader(samples)
	ctx := context.Background()
	pipe := NewAudioPipe(ctx, reader2, 256)
	pipe.Start()

	var piped []uint32
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			piped = append(piped, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Pipe read failed: %v", err)
		}
	}
	pipe.Close()

	// Compare results
	if len(direct) != len(piped) {
		t.Fatalf("Length mismatch: direct=%d, piped=%d", len(direct), len(piped))
	}

	if !bytes.Equal(
		uint32SliceToBytes(direct),
		uint32SliceToBytes(piped),
	) {
		t.Error("Direct read and AudioPipe read produced different results")
	}
}

func uint32SliceToBytes(s []uint32) []byte {
	b := make([]byte, len(s)*4)
	for i, v := range s {
		b[i*4+0] = byte(v)
		b[i*4+1] = byte(v >> 8)
		b[i*4+2] = byte(v >> 16)
		b[i*4+3] = byte(v >> 24)
	}
	return b
}
