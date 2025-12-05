package accuraterip

import (
	"ctdbtools/internal/parity"
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
	// stridecount = ((finalSampleCount - pregap*588) * 2) / stride - 2
	// The -2 accounts for leadin and leadout strides excluded from RS codeword.
	// This matches CueTools CDRepair.cs:30.
	dataSamples := finalSampleCount - pregap*588
	strideCount := 1
	if stride > 0 && dataSamples > 0 {
		strideCount = (dataSamples * 2) / stride - 2
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
			// Process low word (left channel) using LFSR-style encoding
			ps.syndromeCalc(currentPart, uint16(s&0xffff))

			// Process high word (right channel) at next stride position
			nextPart := (currentPart + 1) % ps.Stride
			ps.syndromeCalc(nextPart, uint16(s>>16))
		}

		ps.sampleCount++
	}
}

// syndromeCalc performs LFSR-style Reed-Solomon parity accumulation.
// This mirrors CueTools AccurateRip.cs SyndromeCalc8/SyndromeCalc16.
//
// The algorithm:
//  1. XOR input sample with first parity word
//  2. Look up encode table entries for both bytes of the XORed value
//  3. Shift parity buffer left by 1 position
//  4. XOR in the encode table values
//
// This is systematic RS encoding where each sample contributes to the syndrome
// through polynomial division in GF(2^16).
func (ps *ParityState) syndromeCalc(part int, sample uint16) {
	base := part * ps.MaxNpar * 2

	// Get first parity word and XOR with input sample
	// CueTools: ushort wrlo = (ushort)(wr[0] ^ lo);
	wr0 := uint16(ps.ParityBuf[base]) | uint16(ps.ParityBuf[base+1])<<8
	wrlo := wr0 ^ sample

	// Lookup encode table entries for both bytes
	// CueTools: ushort* ptiblo0 = pt + (wrlo & 255) * maxNpar * 2;
	// CueTools: ushort* ptiblo1 = pt + (wrlo >> 8) * maxNpar * 2 + maxNpar;
	loIdx := int(wrlo & 0xff)
	hiIdx := int(wrlo >> 8)

	// Shift parity buffer left by 1 position and XOR in encode table values
	// CueTools (for maxNpar=8):
	// ((ulong*)wr)[0] = ((ulong*)(wr + 1))[0] ^ ((ulong*)ptiblo0)[0] ^ ((ulong*)ptiblo1)[0];
	// ((ulong*)wr)[1] = (((ulong*)(wr))[1] >> 16) ^ ((ulong*)ptiblo0)[1] ^ ((ulong*)ptiblo1)[1];
	//
	// This shifts the parity array left by one uint16 and XORs in the table values.
	for j := 0; j < ps.MaxNpar-1; j++ {
		nextOff := base + (j+1)*2
		next := uint16(ps.ParityBuf[nextOff]) | uint16(ps.ParityBuf[nextOff+1])<<8
		result := next ^ ps.EncodeTab[loIdx][0][j] ^ ps.EncodeTab[hiIdx][1][j]
		ps.ParityBuf[base+j*2] = byte(result)
		ps.ParityBuf[base+j*2+1] = byte(result >> 8)
	}

	// Last position gets just the table XOR (shifted in zero from the right)
	lastIdx := ps.MaxNpar - 1
	last := ps.EncodeTab[loIdx][0][lastIdx] ^ ps.EncodeTab[hiIdx][1][lastIdx]
	ps.ParityBuf[base+lastIdx*2] = byte(last)
	ps.ParityBuf[base+lastIdx*2+1] = byte(last >> 8)
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

// SyndromeWithOffset adjusts syndrome for drive offset using lead-in/out buffers.
// This mirrors CueTools AccurateRipVerify.GetSyndrome.
//
// The problem: Different CD drives read at different offsets. If we computed
// syndrome at offset 0, but the CTDB entry was submitted at offset +667, the
// syndromes won't match even for identical audio.
//
// The solution: Use lead-in and lead-out buffers to adjust the syndrome.
// - Lead-in buffer: Samples at the start of the disc (before first track data)
// - Lead-out buffer: Samples at the end of the disc (after last track data)
//
// When adjusting for offset:
// - Positive offset: Include samples from lead-out, exclude from lead-in
// - Negative offset: Include samples from lead-in, exclude from lead-out
//
// The adjustment uses Galois field arithmetic to "rotate" the syndrome:
//   synI = g.MulExp(synI, i)  // multiply by α^i
//   synI ^= leadOut[...] ^ g.MulExp(leadIn[...], (i*strideCount)%max)
//
// This allows comparing syndromes computed at different offsets without
// reprocessing the entire audio file.
func (ps *ParityState) SyndromeWithOffset(offset int, strides int) [][]uint16 {
	if strides == -1 || strides == 0 {
		strides = ps.Stride
	}

	// Check offset is within buffer bounds
	// leadIn is at least max(4096*4, stride*2), leadOut is at least max(4096*4, stride+stride)
	maxOffset := len(ps.leadIn) / 4 // Conservative: allow offset*2 up to quarter of buffer
	if offset > maxOffset || offset < -maxOffset {
		return nil // Offset too large for buffer
	}

	syn := parity.Parity2Syndrome(strides, ps.Stride, ps.MaxNpar, ps.MaxNpar, ps.ParityBuf, 0, -offset*2)
	g := parity.Galois16
	// mirror CUETools AccurateRipVerify.GetSyndrome leadin/leadout adjustments
	for part2 := 0; part2 < strides; part2++ {
		part := (part2 + offset*2 + ps.Stride) % ps.Stride
		if part < 0 {
			part += ps.Stride
		}
		if part < offset*2 {
			leadOutIdx := ps.LastStride - part - 1
			leadInIdx := ps.Stride + part
			// Bounds check
			if leadOutIdx < 0 || leadOutIdx >= len(ps.leadOut) || leadInIdx < 0 || leadInIdx >= len(ps.leadIn) {
				continue
			}
			for i := 0; i < ps.MaxNpar; i++ {
				synI := int(syn[part2][i])
				synI = g.MulExp(synI, i)
				synI ^= int(ps.leadOut[leadOutIdx]) ^ g.MulExp(int(ps.leadIn[leadInIdx]), (i*ps.strideCount)%g.MaxVal())
				syn[part2][i] = uint16(synI)
			}
		}
		if part >= ps.Stride+offset*2 {
			leadOutIdx := ps.LastStride + ps.Stride - part - 1
			leadInIdx := part
			// Bounds check
			if leadOutIdx < 0 || leadOutIdx >= len(ps.leadOut) || leadInIdx < 0 || leadInIdx >= len(ps.leadIn) {
				continue
			}
			for i := 0; i < ps.MaxNpar; i++ {
				synI := int(syn[part2][i])
				// subtract leadout and leadin, then divide by a^i
				synI ^= int(ps.leadOut[leadOutIdx]) ^ g.MulExp(int(ps.leadIn[leadInIdx]), (i*ps.strideCount)%g.MaxVal())
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
