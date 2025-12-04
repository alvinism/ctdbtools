package accuraterip

import (
	"ctdbtool/internal/parity"
)

// ParityState holds parity buffers and encode tables for stride-based parity generation.
type ParityState struct {
	Stride     int
	LastStride int
	ParityBuf  []byte
	EncodeTab  [][][]uint16
	MaxNpar    int
}

// NewParityState initializes parity buffer and encode table for given stride/npar.
func NewParityState(stride int, npar int) *ParityState {
	ps := &ParityState{
		Stride:     stride,
		LastStride: stride,
		MaxNpar:    npar,
		ParityBuf:  make([]byte, stride*npar*2),
		EncodeTab:  parity.Galois16.MakeEncodeTable(npar),
	}
	return ps
}

// AddSamples updates parity buffer with a chunk of samples at given track position.
// This is a simplified version; for full fidelity we would mirror CUETools stride/lead-in/out handling.
func (ps *ParityState) AddSamples(samples []uint32, offsetSamples int) {
	// parity bytes are ushort-per-stride position
	for i, s := range samples {
		part := (offsetSamples + i) % ps.Stride
		lo := byte(s & 0xff)
		hi := byte((s >> 8) & 0xff)
		for j := 0; j < ps.MaxNpar; j++ {
			idx := part*ps.MaxNpar*2 + j*2
			cur := uint16(ps.ParityBuf[idx]) | uint16(ps.ParityBuf[idx+1])<<8
			cur ^= ps.EncodeTab[lo][0][j]
			cur ^= ps.EncodeTab[hi][1][j]
			ps.ParityBuf[idx] = byte(cur)
			ps.ParityBuf[idx+1] = byte(cur >> 8)
		}
	}
}

// Syndrome returns the syndrome matrix for current parity buffer.
func (ps *ParityState) Syndrome() [][]uint16 {
	return parity.Parity2Syndrome(ps.Stride, ps.Stride, ps.MaxNpar, ps.MaxNpar, ps.ParityBuf, 0, 0)
}
