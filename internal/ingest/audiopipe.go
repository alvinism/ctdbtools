// Package ingest provides audio decoding utilities.
// This file implements AudioPipe, a goroutine-based buffer similar to CueTools' AudioPipe.
// It decouples audio reading from processing, allowing FFmpeg output to be buffered
// while the main thread calculates CRCs.
package ingest

import (
	"context"
	"io"
)

// audioBuffer holds a chunk of audio samples.
type audioBuffer struct {
	samples []uint32
	n       int   // Number of valid samples
	err     error // Error that occurred during read (including io.EOF)
}

// AudioPipe decouples audio reading from processing using a channel-based buffer.
// Similar to CueTools.Codecs.AudioPipe, it runs a background goroutine that
// reads samples from the source while the main thread processes from a buffer.
//
// This is useful when the source (FFmpeg) can produce data faster than it's processed,
// allowing the decode and CRC calculation to happen in parallel.
type AudioPipe struct {
	reader io.ReadCloser

	// Channel for passing buffers from reader to consumer
	bufCh chan audioBuffer

	// Current buffer being consumed
	current    audioBuffer
	currentPos int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewAudioPipe creates a new audio pipe that reads from the given source.
// The bufferSize is the number of stereo samples per buffer (e.g., 16384).
func NewAudioPipe(ctx context.Context, reader io.ReadCloser, bufferSize int) *AudioPipe {
	pipeCtx, cancel := context.WithCancel(ctx)
	p := &AudioPipe{
		reader: reader,
		bufCh:  make(chan audioBuffer, 2), // Allow 2 buffers in flight
		ctx:    pipeCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	return p
}

// Start begins reading from the source in a background goroutine.
func (p *AudioPipe) Start() {
	go p.readLoop()
}

// readLoop runs in a background goroutine, continuously reading from the source.
func (p *AudioPipe) readLoop() {
	defer close(p.done)
	defer close(p.bufCh)

	bufferSize := 16384
	for {
		// Check for cancellation
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		// Allocate a fresh buffer for each read
		buf := audioBuffer{
			samples: make([]uint32, bufferSize),
		}

		// Read samples
		n, err := PCMChunk(p.reader, buf.samples)
		buf.n = n
		buf.err = err

		// Send to consumer (will block if channel is full)
		select {
		case p.bufCh <- buf:
		case <-p.ctx.Done():
			return
		}

		// Stop on error or EOF
		if err != nil {
			return
		}
	}
}

// Read reads samples from the pipe into the provided buffer.
// Returns the number of samples read and any error.
// Returns io.EOF when all data has been read.
func (p *AudioPipe) Read(buf []uint32) (int, error) {
	// If we have data remaining in current buffer, use it
	if p.currentPos < p.current.n {
		n := copy(buf, p.current.samples[p.currentPos:p.current.n])
		p.currentPos += n
		return n, nil
	}

	// Get next buffer from channel
	select {
	case next, ok := <-p.bufCh:
		if !ok {
			// Channel closed
			return 0, io.EOF
		}
		p.current = next
		p.currentPos = 0

		if p.current.n == 0 {
			// Empty buffer, check for error
			if p.current.err != nil {
				return 0, p.current.err
			}
			return 0, io.EOF
		}

		// Copy data
		n := copy(buf, p.current.samples[p.currentPos:p.current.n])
		p.currentPos += n

		// If this was also the last buffer and has an error, don't return it yet
		// (we'll return it on the next Read when buffer is empty)
		return n, nil

	case <-p.ctx.Done():
		return 0, p.ctx.Err()
	}
}

// Close stops the background reader and releases resources.
func (p *AudioPipe) Close() error {
	p.cancel()
	<-p.done // Wait for readLoop to finish

	if p.reader != nil {
		return p.reader.Close()
	}
	return nil
}
