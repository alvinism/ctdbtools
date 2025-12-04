package cli

import (
	"context"
	"fmt"

	"ctdbtool/internal/ingest"
	"ctdbtool/internal/toc"
)

// VerifyOptions holds parameters for a verify run.
type VerifyOptions struct {
	AudioPath  string
	Layout     toc.Layout
	Stride     int
	LastStride int
	Npar       int
	CalcParity bool
}

// Verify runs a verification pass (decode PCM, compute CRCs/parity).
// CTDB/AccurateRip network lookups are TODO.
func Verify(ctx context.Context, opts VerifyOptions) error {
	proc, err := ingest.ProcessFile(ctx, opts.AudioPath, opts.Layout, opts.Stride, opts.LastStride, opts.Npar, opts.CalcParity)
	if err != nil {
		return err
	}
	fmt.Printf("CRC (track1, offset0): %08x\n", proc.CRC(0))
	fmt.Printf("Syndrome rows: %d\n", len(proc.Syndrome()))
	return nil
}
