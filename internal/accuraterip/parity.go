package accuraterip

import (
	"ctdbtool/internal/parity"
)

// ParityState holds parity buffers and encode tables for stride-based parity generation.
// This mirrors CueTools AccurateRipVerify parity tracking.
type ParityState struct {
	Stride     int
	LastStride int
	ParityBuf  []byte
	EncodeTab  [][][]uint16
	MaxNpar    int

	leadIn      []uint16
	leadOut     []uint16
	strideCount int // (finalSampleCount - pregap*588) * 2 / stride

	// Tracking state from CueTools
	pregap           int // in frames (typically TOC.Pregap)
	finalSampleCount int // total audio samples (AudioLength * 588)
	sampleCount      int // current sample position (0-based, includes pregap)
}

// NewParityState initializes parity buffer and encode table for given stride/npar.
// pregap is in frames, finalSampleCount is total audio samples (AudioLength * 588).
func NewParityState(stride, npar, pregap, finalSampleCount int) *ParityState {
	// stridecount = (finalSampleCount - pregap*588) * 2 / stride
	// This is the number of strides in the parity window.
	dataSamples := finalSampleCount - pregap*588
	strideCount := 1
	if stride > 0 && dataSamples > 0 {
		strideCount = (dataSamples * 2) / stride
	}

	ps := &ParityState{
		Stride:           stride,
		LastStride:       stride,
		MaxNpar:          npar,
		ParityBuf:        make([]byte, stride*npar*2),
		EncodeTab:        parity.Galois16.MakeEncodeTable(npar),
		leadIn:           make([]uint16, maxInt(4096*4, stride*2)),
		leadOut:          make([]uint16, maxInt(4096*4, stride+stride)),
		strideCount:      strideCount,
		pregap:           pregap,
		finalSampleCount: finalSampleCount,
		sampleCount:      0,
	}
	return ps
}

// AddSamples updates parity buffer with a chunk of samples.
// This mirrors CueTools AccurateRipVerify.Write and CalculateCRCs.
//
// The parity window is controlled by currentStride:
//   - currentSample = sampleCount - pregap*588 (can be negative in pregap)
//   - currentStride = (currentSample * 2) / stride
//   - Parity is accumulated when 1 <= currentStride <= stridecount
//
// Lead-in/out buffers use word-based indexing matching CueTools:
//   - Lead-in: index = currentSample*2 + wordOffset
//   - Lead-out: index = (finalSampleCount - sampleCount)*2 - wordOffset - 1
func (ps *ParityState) AddSamples(samples []uint32) {
	for _, s := range samples {
		// CueTools: currentSample = _sampleCount - 588 * TOC.Pregap
		currentSample := ps.sampleCount - ps.pregap*588

		// Fill lead-in buffer (CueTools: AccurateRip.cs:611-612)
		// for (int i = Math.Max(0, -currentSample*2); i < Math.Min(leadin.Length - currentSample*2, copyCount*2); i++)
		//     leadin[currentSample * 2 + i] = ((ushort*)samples)[i];
		wordStart := maxInt(0, -currentSample*2)
		wordEnd := minInt(len(ps.leadIn)-currentSample*2, 2) // 2 words per sample
		for wi := wordStart; wi < wordEnd; wi++ {
			pos := currentSample*2 + wi
			if pos >= 0 && pos < len(ps.leadIn) {
				if wi == 0 {
					ps.leadIn[pos] = uint16(s & 0xffff)
				} else {
					ps.leadIn[pos] = uint16(s >> 16)
				}
			}
		}

		// Fill lead-out buffer (CueTools: AccurateRip.cs:614-618)
		// for (int i = Math.Max(0, (finalSampleCount - sampleCount)*2 - leadout.Length); i < copyCount*2; i++)
		//     remaining = (finalSampleCount - sampleCount)*2 - i - 1
		//     leadout[remaining] = ((ushort*)samples)[i];
		remainingWords := (ps.finalSampleCount - ps.sampleCount) * 2
		wordStart = maxInt(0, remainingWords-len(ps.leadOut))
		for wi := wordStart; wi < 2; wi++ {
			remaining := remainingWords - wi - 1
			if remaining >= 0 && remaining < len(ps.leadOut) {
				if wi == 0 {
					ps.leadOut[remaining] = uint16(s & 0xffff)
				} else {
					ps.leadOut[remaining] = uint16(s >> 16)
				}
			}
		}

		// CueTools: currentPart = currentSample < 0 ? 0 : (currentSample * 2) % stride
		currentPart := 0
		if currentSample >= 0 {
			currentPart = (currentSample * 2) % ps.Stride
		}

		// CueTools: currentStride = (currentSample * 2) / stride
		// doPar = currentStride >= 1 && currentStride <= stridecount && calcParity
		currentStride := 0
		if currentSample > 0 && ps.Stride > 0 {
			currentStride = (currentSample * 2) / ps.Stride
		}
		doParity := currentStride >= 1 && currentStride <= ps.strideCount

		if doParity {
			// Process low word (left channel)
			lo := byte(s & 0xff)
			loHi := byte((s >> 8) & 0xff)
			part := currentPart
			for j := 0; j < ps.MaxNpar; j++ {
				idx := part*ps.MaxNpar*2 + j*2
				cur := uint16(ps.ParityBuf[idx]) | uint16(ps.ParityBuf[idx+1])<<8
				cur ^= ps.EncodeTab[lo][0][j]
				cur ^= ps.EncodeTab[loHi][1][j]
				ps.ParityBuf[idx] = byte(cur)
				ps.ParityBuf[idx+1] = byte(cur >> 8)
			}

			// Process high word (right channel)
			hi := byte((s >> 16) & 0xff)
			hiHi := byte((s >> 24) & 0xff)
			part = (currentPart + 1) % ps.Stride
			for j := 0; j < ps.MaxNpar; j++ {
				idx := part*ps.MaxNpar*2 + j*2
				cur := uint16(ps.ParityBuf[idx]) | uint16(ps.ParityBuf[idx+1])<<8
				cur ^= ps.EncodeTab[hi][0][j]
				cur ^= ps.EncodeTab[hiHi][1][j]
				ps.ParityBuf[idx] = byte(cur)
				ps.ParityBuf[idx+1] = byte(cur >> 8)
			}
		}

		ps.sampleCount++
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
