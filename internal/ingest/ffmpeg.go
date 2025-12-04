package ingest

import (
	"context"
	"io"
	"os/exec"
)

// PCMStream invokes ffmpeg to decode an input file to 16-bit stereo PCM @ 44.1kHz, returning a reader.
func PCMStream(ctx context.Context, input string) (io.ReadCloser, *exec.Cmd, error) {
	args := []string{
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
