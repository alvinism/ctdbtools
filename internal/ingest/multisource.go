package ingest

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
)

// MultiSourceReader provides sequential audio reading across multiple source segments.
// It automatically switches between files as each source is exhausted.
type MultiSourceReader struct {
	ctx           context.Context
	sources       []SourceSegment
	cueDir        string
	currentIdx    int
	currentStream io.ReadCloser
	currentCmd    *exec.Cmd
	samplesRead   int64 // samples read from current source
	totalRead     int64 // total samples read across all sources
	debug         bool  // enable debug output
}

// NewMultiSourceReader creates a reader that will read audio from multiple source segments.
func NewMultiSourceReader(ctx context.Context, sources []SourceSegment, cueDir string) *MultiSourceReader {
	return &MultiSourceReader{
		ctx:        ctx,
		sources:    sources,
		cueDir:     cueDir,
		currentIdx: -1, // not started yet
	}
}

// openNextSource closes the current source and opens the next one.
func (r *MultiSourceReader) openNextSource() error {
	// Close current source if any
	r.closeCurrentSource()

	r.currentIdx++
	if r.currentIdx >= len(r.sources) {
		return io.EOF // no more sources
	}

	src := r.sources[r.currentIdx]
	if src.FilePath == "" {
		// Empty file path means silence/pregap - skip
		return r.openNextSource()
	}

	// Resolve path relative to CUE directory
	audioPath := src.FilePath
	if !filepath.IsAbs(audioPath) && r.cueDir != "" {
		audioPath = filepath.Join(r.cueDir, audioPath)
	}

	// Open FFmpeg stream
	stream, cmd, err := PCMStream(r.ctx, audioPath)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", audioPath, err)
	}

	r.currentStream = stream
	r.currentCmd = cmd
	r.samplesRead = 0

	// Handle offset: skip samples if needed
	if src.Offset > 0 {
		// Skip offset samples by reading and discarding
		skipBuf := make([]uint32, 4096)
		toSkip := src.Offset
		for toSkip > 0 {
			n := int64(len(skipBuf))
			if n > toSkip {
				n = toSkip
			}
			read, err := PCMChunk(r.currentStream, skipBuf[:n])
			if err != nil && err != io.EOF {
				return fmt.Errorf("failed to skip offset in %s: %w", audioPath, err)
			}
			toSkip -= int64(read)
			if err == io.EOF {
				break
			}
		}
	}

	return nil
}

// closeCurrentSource cleans up the current FFmpeg process.
func (r *MultiSourceReader) closeCurrentSource() {
	if r.currentStream != nil {
		r.currentStream.Close()
		r.currentStream = nil
	}
	if r.currentCmd != nil {
		r.currentCmd.Process.Kill()
		r.currentCmd.Wait()
		r.currentCmd = nil
	}
}

// Read fills buf with stereo samples from the sources.
// It automatically switches to the next source when the current one is exhausted.
// Returns the number of samples read and any error.
func (r *MultiSourceReader) Read(buf []uint32) (int, error) {
	if r.currentIdx < 0 {
		// First read - open first source
		if err := r.openNextSource(); err != nil {
			return 0, err
		}
	}

	totalRead := 0
	remaining := len(buf)

	for remaining > 0 {
		if r.currentStream == nil {
			// No current source - we're done
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, io.EOF
		}

		// Check if we've read enough from this source
		src := r.sources[r.currentIdx]
		if src.Length > 0 && r.samplesRead >= src.Length {
			if r.debug {
				fmt.Printf("DEBUG MultiSourceReader: Source %d exhausted (%d samples), switching to next\n",
					r.currentIdx, r.samplesRead)
			}
			// Current source exhausted, move to next
			if err := r.openNextSource(); err != nil {
				if err == io.EOF && totalRead > 0 {
					return totalRead, nil
				}
				return totalRead, err
			}
			continue
		}

		// Calculate how many samples to read
		toRead := remaining
		if src.Length > 0 {
			maxFromSource := src.Length - r.samplesRead
			if int64(toRead) > maxFromSource {
				toRead = int(maxFromSource)
			}
		}

		// Read from current source
		n, err := PCMChunk(r.currentStream, buf[totalRead:totalRead+toRead])
		r.samplesRead += int64(n)
		r.totalRead += int64(n)
		totalRead += n
		remaining -= n

		if err == io.EOF {
			if r.debug {
				fmt.Printf("DEBUG MultiSourceReader: Source %d file EOF at %d samples (expected %d)\n",
					r.currentIdx, r.samplesRead, src.Length)
			}
			// Current file exhausted, try next source
			if err := r.openNextSource(); err != nil {
				if err == io.EOF {
					// No more sources
					if totalRead > 0 {
						return totalRead, nil
					}
					return 0, io.EOF
				}
				return totalRead, err
			}
		} else if err != nil {
			return totalRead, err
		}
	}

	return totalRead, nil
}

// SetDebug enables/disables debug output
func (r *MultiSourceReader) SetDebug(enabled bool) {
	r.debug = enabled
}

// Close releases all resources.
func (r *MultiSourceReader) Close() error {
	r.closeCurrentSource()
	return nil
}

// TotalSamplesRead returns the total number of samples read across all sources.
func (r *MultiSourceReader) TotalSamplesRead() int64 {
	return r.totalRead
}
