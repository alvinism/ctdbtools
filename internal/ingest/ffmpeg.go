package ingest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// CheckDependencies verifies that required external tools (ffmpeg, ffprobe) are available.
// Returns an error with a helpful message if any dependency is missing.
func CheckDependencies() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH: please install ffmpeg, if you already have it installed, ensure it is in your PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return fmt.Errorf("ffprobe not found in PATH: please install ffmpeg, if you already have it installed, ensure it is in your PATH")
	}
	return nil
}

// PCMStream invokes ffmpeg to decode an input file to 16-bit stereo PCM @ 44.1kHz, returning a reader.
func PCMStream(ctx context.Context, input string) (io.ReadCloser, *exec.Cmd, error) {
	args := []string{
		"-threads", "0",          // Auto-detect optimal thread count
		"-probesize", "32768",    // Reduce from 5MB default for faster format detection
		"-analyzeduration", "500000", // 500ms max analysis time
		"-v", "quiet",
		"-i", input,
		"-f", "s16le",
		"-ac", "2",
		"-ar", "44100",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return stdout, cmd, nil
}

// PCMChunk reads little-endian stereo samples from reader into a uint32 slice (L low16 | R high16).
// count is number of stereo samples to read.
func PCMChunk(r io.Reader, buf []uint32) (int, error) {
	byteBuf := make([]byte, len(buf)*4)
	n, err := io.ReadFull(r, byteBuf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return 0, err
	}
	samples := n / 4
	for i := 0; i < samples; i++ {
		lo := uint32(byteBuf[i*4]) | uint32(byteBuf[i*4+1])<<8
		hi := uint32(byteBuf[i*4+2]) | uint32(byteBuf[i*4+3])<<8
		buf[i] = lo | hi<<16
	}
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return samples, io.EOF
	}
	return samples, nil
}

// ProbeSampleRate uses ffprobe to get the sample rate of an audio file in Hz.
func ProbeSampleRate(ctx context.Context, input string) (int, error) {
	args := []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=sample_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	}
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	rateStr := strings.TrimSpace(out.String())
	rate, err := strconv.Atoi(rateStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse sample rate %q: %w", rateStr, err)
	}
	return rate, nil
}

// ProbeBitDepth uses ffprobe to get the bit depth of an audio file.
func ProbeBitDepth(ctx context.Context, input string) (int, error) {
	args := []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=bits_per_raw_sample",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	}
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	depthStr := strings.TrimSpace(out.String())
	depth, err := strconv.Atoi(depthStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse bit depth %q: %w", depthStr, err)
	}
	return depth, nil
}

// ProbeEffectiveBitDepth decodes a short segment of audio as s32le and determines
// the effective bit depth by checking which bits are actually used.
// This detects padded audio (e.g., 16-bit CD audio stored in a 24-bit container).
func ProbeEffectiveBitDepth(ctx context.Context, input string) (int, error) {
	args := []string{
		"-v", "error",
		"-i", input,
		"-t", "1",
		"-f", "s32le",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to decode audio: %w", err)
	}
	if len(out) < 4 {
		return 0, fmt.Errorf("no audio samples decoded from %s", input)
	}

	// OR all sample values to build a bitmask of used bits
	var mask uint32
	for i := 0; i+3 < len(out); i += 4 {
		v := uint32(out[i]) | uint32(out[i+1])<<8 | uint32(out[i+2])<<16 | uint32(out[i+3])<<24
		mask |= v
	}

	// In s32le, ffmpeg left-aligns samples.
	// 16-bit audio: low 16 bits are always zero.
	// 24-bit audio: low 8 bits are always zero, but bits 8-15 may be non-zero.
	if mask&0xFFFF == 0 {
		return 16, nil
	}
	if mask&0xFF == 0 {
		return 24, nil
	}
	return 32, nil
}

// ValidateCDFormat checks that an audio file is in CD format (44100 Hz, 16-bit).
// Returns nil if valid, or an error with a descriptive message if not.
func ValidateCDFormat(ctx context.Context, path string) error {
	rate, err := ProbeSampleRate(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to probe audio format of %s: %w", path, err)
	}
	if rate != 44100 {
		return fmt.Errorf("source audio is not in CD format: expected 44100 Hz sample rate, got %d Hz (%s)", rate, path)
	}
	// Fast path: if metadata says 16-bit, no need to decode
	depth, err := ProbeBitDepth(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to probe audio format of %s: %w", path, err)
	}
	if depth == 16 {
		return nil
	}
	// Container is >16 bit — check if audio data is effectively 16-bit (padded)
	effDepth, err := ProbeEffectiveBitDepth(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to probe audio format of %s: %w", path, err)
	}
	if effDepth > 16 {
		return fmt.Errorf("source audio is not in CD format: expected 16-bit audio, got %d-bit (%s)", effDepth, path)
	}
	return nil
}

// ProbeSampleCount uses ffprobe to get the exact number of samples in an audio file.
// Unlike ProbeDurationFrames (which rounds to CD frames), this returns the exact sample count
// from container metadata via duration_ts (e.g., FLAC STREAMINFO, WAV data chunk size).
// No floating-point rounding is involved.
func ProbeSampleCount(ctx context.Context, input string) (int64, error) {
	args := []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=duration_ts",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	}
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	tsStr := strings.TrimSpace(out.String())
	samples, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration_ts %q: %w", tsStr, err)
	}
	return samples, nil
}

// ProbeDurationFrames uses ffprobe to get the duration of an audio file in CD frames (1/75 sec).
func ProbeDurationFrames(ctx context.Context, input string) (int, error) {
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	}
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	durStr := strings.TrimSpace(out.String())
	dur, err := strconv.ParseFloat(durStr, 64)
	if err != nil {
		return 0, err
	}
	// Convert seconds to CD frames (75 Hz)
	frames := int(dur*75 + 0.5) // round to nearest
	return frames, nil
}
