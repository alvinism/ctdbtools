package audio

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// FLACWriter writes RedBook-quality audio to a FLAC file.
// Output format: 44.1kHz, 16-bit, stereo (CD quality).
// Uses external flac or ffmpeg encoder via stdin pipe.
type FLACWriter struct {
	path        string
	compression int
	useFFmpeg   bool
	metadata    *Metadata

	cmd       *exec.Cmd
	stdin     io.WriteCloser
	buf       *bufio.Writer // Buffered wrapper around stdin
	stderr    *bytes.Buffer // Captured stderr for error reporting
	sampleCnt int64

	// For error handling from encoder process
	cmdErr    error
	cmdErrMu  sync.Mutex
	cmdDone   chan struct{}
	closeOnce sync.Once
}

// Compile-time check that FLACWriter implements AudioWriter
var _ AudioWriter = (*FLACWriter)(nil)

// NewFLACWriter creates a new FLAC writer that encodes via external process.
// compression: FLAC compression level 0-8 (8 is default)
// useFFmpeg: if true, use ffmpeg; if false, use native flac encoder
// metadata: optional metadata to embed in the file
func NewFLACWriter(path string, compression int, useFFmpeg bool, metadata *Metadata) (*FLACWriter, error) {
	if compression < 0 || compression > 8 {
		compression = 8
	}

	w := &FLACWriter{
		path:        path,
		compression: compression,
		useFFmpeg:   useFFmpeg,
		metadata:    metadata,
		cmdDone:     make(chan struct{}),
	}

	if err := w.startEncoder(); err != nil {
		return nil, err
	}

	return w, nil
}

// startEncoder starts the external encoder process.
func (w *FLACWriter) startEncoder() error {
	var cmd *exec.Cmd

	if w.useFFmpeg {
		// FFmpeg: read raw PCM from stdin, write FLAC
		// -y: overwrite output
		// -f s16le: input format is signed 16-bit little-endian
		// -ar 44100: sample rate 44.1kHz
		// -ac 2: 2 channels (stereo)
		// -i pipe:0: read from stdin
		// -compression_level N: FLAC compression
		args := []string{
			"-y",
			"-f", "s16le",
			"-ar", "44100",
			"-ac", "2",
			"-i", "pipe:0",
			"-compression_level", fmt.Sprintf("%d", w.compression),
		}
		// Add metadata if provided
		if w.metadata != nil {
			args = append(args, w.metadata.ToFFmpegArgs()...)
		}
		args = append(args, w.path)
		cmd = exec.Command("ffmpeg", args...)
	} else {
		// Native flac encoder: read raw PCM from stdin
		// --endian=little: input is little-endian
		// --channels=2: stereo
		// --bps=16: 16 bits per sample
		// --sample-rate=44100: 44.1kHz
		// --sign=signed: signed samples
		// -V: verify encoding
		// -f: force overwrite
		// -compression-level-N: compression level
		// -o output.flac: output file
		// -: read from stdin
		cmd = exec.Command("flac",
			"--endian=little",
			"--channels=2",
			"--bps=16",
			"--sample-rate=44100",
			"--sign=signed",
			"-V",
			"-f",
			fmt.Sprintf("-%d", w.compression),
			"-o", w.path,
			"-",
		)
		// Note: metadata will be applied via metaflac after encoding
	}

	// Capture stderr for error reporting (only shown on failure)
	w.stderr = &bytes.Buffer{}
	cmd.Stderr = w.stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start encoder: %w", err)
	}

	w.cmd = cmd
	w.stdin = stdin
	w.buf = bufio.NewWriterSize(stdin, 256*1024) // 256KB buffer like WAVWriter

	// Monitor process completion in background
	go func() {
		err := cmd.Wait()
		w.cmdErrMu.Lock()
		w.cmdErr = err
		w.cmdErrMu.Unlock()
		close(w.cmdDone)
	}()

	return nil
}

// WriteSample writes a single stereo sample (32-bit packed: L16|R16).
func (w *FLACWriter) WriteSample(sample uint32) error {
	var b [4]byte
	b[0] = byte(sample)
	b[1] = byte(sample >> 8)
	b[2] = byte(sample >> 16)
	b[3] = byte(sample >> 24)

	_, err := w.buf.Write(b[:])
	if err != nil {
		return fmt.Errorf("failed to write sample: %w", err)
	}

	w.sampleCnt++
	return nil
}

// WriteSamples writes multiple stereo samples.
func (w *FLACWriter) WriteSamples(samples []uint32) error {
	for _, sample := range samples {
		if err := w.WriteSample(sample); err != nil {
			return err
		}
	}
	return nil
}

// WriteSamplesBulk writes multiple stereo samples efficiently using a pre-allocated buffer.
// This is faster than WriteSamples for large batches.
func (w *FLACWriter) WriteSamplesBulk(samples []uint32) error {
	buf := make([]byte, len(samples)*4)
	for i, sample := range samples {
		buf[i*4] = byte(sample)
		buf[i*4+1] = byte(sample >> 8)
		buf[i*4+2] = byte(sample >> 16)
		buf[i*4+3] = byte(sample >> 24)
	}

	_, err := w.buf.Write(buf)
	if err != nil {
		return fmt.Errorf("failed to write samples: %w", err)
	}
	w.sampleCnt += int64(len(samples))
	return nil
}

// Write16BitSample writes a single 16-bit sample (one channel).
func (w *FLACWriter) Write16BitSample(sample uint16) error {
	var b [2]byte
	b[0] = byte(sample)
	b[1] = byte(sample >> 8)

	_, err := w.buf.Write(b[:])
	if err != nil {
		return fmt.Errorf("failed to write 16-bit sample: %w", err)
	}

	return nil
}

// WriteBytes writes raw bytes to the encoder.
func (w *FLACWriter) WriteBytes(data []byte) error {
	_, err := w.buf.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write bytes: %w", err)
	}
	// Update sample count (4 bytes per stereo sample)
	w.sampleCnt += int64(len(data) / 4)
	return nil
}

// SampleCount returns the number of stereo samples written.
func (w *FLACWriter) SampleCount() int64 {
	return w.sampleCnt
}

// DataSize returns the current data size in bytes (uncompressed PCM).
func (w *FLACWriter) DataSize() uint32 {
	return uint32(w.sampleCnt * 4)
}

// Close finalizes the FLAC file.
func (w *FLACWriter) Close() error {
	var closeErr error

	w.closeOnce.Do(func() {
		// Flush buffer before closing stdin
		if w.buf != nil {
			if err := w.buf.Flush(); err != nil {
				closeErr = fmt.Errorf("failed to flush buffer: %w", err)
				return
			}
		}

		// Close stdin to signal end of input
		if w.stdin != nil {
			if err := w.stdin.Close(); err != nil {
				closeErr = fmt.Errorf("failed to close encoder stdin: %w", err)
				return
			}
		}

		// Wait for encoder to finish
		<-w.cmdDone

		// Check encoder exit status
		w.cmdErrMu.Lock()
		cmdErr := w.cmdErr
		w.cmdErrMu.Unlock()

		if cmdErr != nil {
			// Clean up partial output file
			os.Remove(w.path)
			// Include stderr output in error message if available
			if w.stderr != nil && w.stderr.Len() > 0 {
				closeErr = fmt.Errorf("encoder failed: %w\n%s", cmdErr, w.stderr.String())
			} else {
				closeErr = fmt.Errorf("encoder failed: %w", cmdErr)
			}
			return
		}

		// Apply metadata via metaflac for native encoder
		// (FFmpeg applies metadata during encoding)
		if !w.useFFmpeg && w.metadata != nil && !w.metadata.IsEmpty() {
			if err := applyMetadataViaMetaflac(w.path, w.metadata); err != nil {
				// Log warning but don't fail - the audio is encoded correctly
				fmt.Fprintf(os.Stderr, "Warning: failed to apply metadata: %v\n", err)
			}
		}
	})

	return closeErr
}

// Path returns the output file path.
func (w *FLACWriter) Path() string {
	return w.path
}

// SetMetadata sets metadata to be written to the FLAC file.
// For FFmpeg encoder: must be called before encoding starts (before any Write calls).
// For native flac encoder: can be called anytime before Close().
func (w *FLACWriter) SetMetadata(meta *Metadata) error {
	w.metadata = meta
	return nil
}

// applyMetadataViaMetaflac applies metadata to a FLAC file using metaflac.
// Uses a temp file for tag values to avoid shell escaping issues.
func applyMetadataViaMetaflac(path string, meta *Metadata) error {
	if !HasMetaflac() {
		return fmt.Errorf("metaflac not found in PATH")
	}

	// Write tags to temp file
	tagContent := meta.ToVorbisCommentFormat()
	if tagContent == "" {
		return nil // No tags to apply
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "tags-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(tagContent); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write tags: %w", err)
	}
	tmpFile.Close()

	// Apply tags with metaflac
	cmd := exec.Command("metaflac",
		"--remove-all-tags",
		"--import-tags-from="+tmpPath,
		path)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("metaflac failed: %w", err)
	}

	return nil
}
