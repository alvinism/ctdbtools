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
		"-threads", "0", // Auto-detect optimal thread count
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
