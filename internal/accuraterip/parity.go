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

	leadIn      []uint16
	leadOut     []uint16
	strideCount int
}

// NewParityState initializes parity buffer and encode table for given stride/npar.
func NewParityState(stride int, npar int) *ParityState {
	ps := &ParityState{
		Stride:      stride,
		LastStride:  stride,
		MaxNpar:     npar,
		ParityBuf:   make([]byte, stride*npar*2),
		EncodeTab:   parity.Galois16.MakeEncodeTable(npar),
		leadIn:      make([]uint16, maxInt(4096*4, stride*2)),
		leadOut:     make([]uint16, maxInt(4096*4, stride+stride)),
		strideCount: 1,
	}
	return ps
}

// AddSamples updates parity buffer with a chunk of samples at given track position.
// This is a simplified version; for full fidelity we would mirror CUETools stride/lead-in/out handling.
func (ps *ParityState) AddSamples(samples []uint32, offsetSamples int, totalSamples int) {
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

		// fill lead-in (store words)
		sampleIndex := offsetSamples + i
		if sampleIndex*2 < len(ps.leadIn) {
			pos := sampleIndex * 2
			ps.leadIn[pos] = uint16(s & 0xffff)
			ps.leadIn[pos+1] = uint16(s >> 16)
		}
		// fill lead-out (store from end)
		remaining := totalSamples - (sampleIndex + 1)
		if remaining*2 < len(ps.leadOut) {
			pos := remaining * 2
			ps.leadOut[pos] = uint16(s & 0xffff)
			ps.leadOut[pos+1] = uint16(s >> 16)
		}
	}
}

// Syndrome returns the syndrome matrix for current parity buffer.
func (ps *ParityState) Syndrome() [][]uint16 {
	return parity.Parity2Syndrome(ps.Stride, ps.Stride, ps.MaxNpar, ps.MaxNpar, ps.ParityBuf, 0, 0)
}

// SyndromeWithOffset adjusts syndrome for drive offset using lead-in/out buffers similar to CUETools AccurateRip.GetSyndrome.
func (ps *ParityState) SyndromeWithOffset(offset int, strides int) [][]uint16 {
	if strides == -1 || strides == 0 {
		strides = ps.Stride
	}
	syn := parity.Parity2Syndrome(strides, ps.Stride, ps.MaxNpar, ps.MaxNpar, ps.ParityBuf, 0, -offset*2)
	g := parity.Galois16
	// mirror CUETools AccurateRipVerify.GetSyndrome leadin/leadout adjustments
	for part2 := 0; part2 < strides; part2++ {
		part := (part2 + offset*2 + ps.Stride) % ps.Stride
		if part < offset*2 {
			for i := 0; i < ps.MaxNpar; i++ {
				synI := int(syn[part2][i])
				synI = g.MulExp(synI, i)
				synI ^= int(ps.leadOut[ps.LastStride-part-1]) ^ g.MulExp(int(ps.leadIn[ps.Stride+part]), (i*ps.strideCount)%g.MaxVal())
				syn[part2][i] = uint16(synI)
			}
		}
		if part >= ps.Stride+offset*2 {
			for i := 0; i < ps.MaxNpar; i++ {
				synI := int(syn[part2][i])
				// subtract leadout and leadin, then divide by a^i
				synI ^= int(ps.leadOut[ps.LastStride+ps.Stride-part-1]) ^ g.MulExp(int(ps.leadIn[part]), (i*ps.strideCount)%g.MaxVal())
				synI = g.DivExp(synI, i)
				syn[part2][i] = uint16(synI)
			}
		}
	}
	return syn
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
