package audio

import (
	"fmt"
	"os/exec"
)

// OutputFormat specifies the audio output format.
type OutputFormat string

const (
	FormatWAV  OutputFormat = "wav"
	FormatFLAC OutputFormat = "flac"
)

// ParseOutputFormat parses a string into an OutputFormat.
func ParseOutputFormat(s string) (OutputFormat, error) {
	switch s {
	case "wav", "WAV":
		return FormatWAV, nil
	case "flac", "FLAC":
		return FormatFLAC, nil
	default:
		return "", fmt.Errorf("unsupported output format: %s (supported: wav, flac)", s)
	}
}

// Extension returns the file extension for this format (including dot).
func (f OutputFormat) Extension() string {
	switch f {
	case FormatFLAC:
		return ".flac"
	default:
		return ".wav"
	}
}

// EncoderPreference specifies which encoder to prefer for FLAC output.
type EncoderPreference string

const (
	EncoderAuto   EncoderPreference = "auto"   // Auto-detect: prefer native, fallback to ffmpeg
	EncoderNative EncoderPreference = "native" // Native flac encoder only
	EncoderFFmpeg EncoderPreference = "ffmpeg" // FFmpeg encoder only
)

// ParseEncoderPreference parses a string into an EncoderPreference.
func ParseEncoderPreference(s string) (EncoderPreference, error) {
	switch s {
	case "auto", "":
		return EncoderAuto, nil
	case "native":
		return EncoderNative, nil
	case "ffmpeg":
		return EncoderFFmpeg, nil
	default:
		return "", fmt.Errorf("unsupported encoder preference: %s (supported: auto, native, ffmpeg)", s)
	}
}

// WriterOptions configures audio output writer creation.
type WriterOptions struct {
	Format      OutputFormat
	Encoder     EncoderPreference
	Metadata    *Metadata
	Compression int // FLAC compression level 0-8, default 8
}

// NewWriter creates an AudioWriter for the specified format.
// For FLAC format, it selects encoder based on EncoderPreference:
// - EncoderAuto: prefer native flac, fallback to ffmpeg
// - EncoderNative: native flac only, error if not available
// - EncoderFFmpeg: ffmpeg only, error if not available
func NewWriter(path string, opts WriterOptions) (AudioWriter, error) {
	switch opts.Format {
	case FormatFLAC:
		return newFLACWriter(path, opts)
	case FormatWAV, "":
		return NewWAVWriter(path)
	default:
		return nil, fmt.Errorf("unsupported output format: %s", opts.Format)
	}
}

// newFLACWriter creates a FLAC writer with appropriate encoder selection.
func newFLACWriter(path string, opts WriterOptions) (AudioWriter, error) {
	compression := opts.Compression
	if compression == 0 {
		compression = 8 // Default FLAC compression level (max)
	}

	useFFmpeg := false
	switch opts.Encoder {
	case EncoderNative:
		if !HasNativeFLACEncoder() {
			return nil, fmt.Errorf("native flac encoder not found in PATH (required by --encoder native)")
		}
	case EncoderFFmpeg:
		if !HasFFmpeg() {
			return nil, fmt.Errorf("ffmpeg not found in PATH (required by --encoder ffmpeg)")
		}
		useFFmpeg = true
	case EncoderAuto, "":
		if HasNativeFLACEncoder() {
			useFFmpeg = false
		} else if HasFFmpeg() {
			useFFmpeg = true
		} else {
			return nil, fmt.Errorf("no FLAC encoder available: install flac or ffmpeg")
		}
	}

	return NewFLACWriter(path, compression, useFFmpeg, opts.Metadata)
}

// HasNativeFLACEncoder checks if the native flac encoder is available in PATH.
func HasNativeFLACEncoder() bool {
	_, err := exec.LookPath("flac")
	return err == nil
}

// HasMetaflac checks if metaflac is available in PATH.
func HasMetaflac() bool {
	_, err := exec.LookPath("metaflac")
	return err == nil
}

// HasFFmpeg checks if ffmpeg is available in PATH.
func HasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}
