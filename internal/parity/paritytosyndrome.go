package parity

// ParityToSyndrome converts between parity bytes and syndromes, mirroring CUETools.Parity.ParityToSyndrome.
type ParityToSyndrome struct {
	erasuresPos   []int
	erasureLocPol []int
	erasureDiff   []int
	npar          int
}

func NewParityToSyndrome(npar int) *ParityToSyndrome {
	p := &ParityToSyndrome{npar: npar}
	p.initTables()
	return p
}

func (p *ParityToSyndrome) initTables() {
	numErasures := p.npar
	p.erasuresPos = make([]int, numErasures)
	for x := 0; x < numErasures; x++ {
		p.erasuresPos[x] = x
	}
	// erasure locator polynomial (log domain)
	erasureLocExp := make([]int, numErasures+1)
	erasureLocExp[0] = 1
	for i := 0; i < numErasures; i++ {
		for x := numErasures; x > 0; x-- {
			erasureLocExp[x] ^= Galois16.mulExp(erasureLocExp[x-1], p.erasuresPos[i])
		}
	}
	p.erasureLocPol = Galois16.toLogSlice(erasureLocExp)
	p.erasureDiff = Galois16.gfdiff(p.erasureLocPol)
}

// Syndrome2Bytes flattens a syndrome matrix to byte slice (column-major per CUETools).
func Syndrome2Bytes(in [][]uint16) []byte {
	stride := len(in)
	if stride == 0 {
		return nil
	}
	npar := len(in[0])
	out := make([]byte, npar*stride*2)
	pos := 0
	for i := 0; i < npar; i++ {
		for j := 0; j < stride; j++ {
			val := in[j][i]
			out[pos] = byte(val)
			out[pos+1] = byte(val >> 8)
			pos += 2
		}
	}
	return out
}

// Bytes2Syndrome converts parity bytes into syndrome matrix.
// Matches CueTools ParityToSyndrome.Bytes2Syndrome:
//   ppar[j + i * stride] -> psyn[i + j * npar]
// Parity bytes are stored column-major: npar groups of stride uint16 values.
func Bytes2Syndrome(stride, npar int, parity []byte) [][]uint16 {
	if len(parity) < npar*stride*2 {
		return nil
	}
	syn := make([][]uint16, stride)
	for i := 0; i < stride; i++ {
		syn[i] = make([]uint16, npar)
	}
	pos := 0
	for i := 0; i < npar; i++ {
		for j := 0; j < stride; j++ {
			lo := uint16(parity[pos])
			hi := uint16(parity[pos+1])
			syn[j][i] = lo | hi<<8
			pos += 2
		}
	}
	return syn
}

// Parity2Syndrome converts parity (with stride2/npar2) into a reduced syndrome (stride/npar), with optional row offset.
func Parity2Syndrome(stride, stride2, npar, npar2 int, parity []byte, pos, offset int) [][]uint16 {
	if npar > npar2 || stride > stride2 {
		return nil
	}
	syn := make([][]uint16, stride)
	for y := 0; y < stride; y++ {
		syn[y] = make([]uint16, npar)
	}
	// interpret parity as ushort matrix stride2 x npar2 (row-major)
	for y := 0; y < stride; y++ {
		y1 := (y - offset + stride2) % stride2
		rowBase := pos + y1*npar2*2
		for x1 := 0; x1 < npar2; x1++ {
			lo := uint16(parity[rowBase+x1*2])
			hi := uint16(parity[rowBase+x1*2+1])
			if lo == 0 && hi == 0 {
				continue
			}
			val := lo | hi<<8
			llo := int(Galois16.logTbl[val]) + 0xffff
			for x := 0; x < npar; x++ {
				syn[y][x] ^= Galois16.expTbl[llo-(1+x1)*x]
			}
		}
	}
	return syn
}

// Syndrome2Parity recovers parity bytes from a syndrome row (single stride row).
// Returns parity bytes for that row (length npar*2).
func (p *ParityToSyndrome) SyndromeRowToParity(syndrome []uint16) []uint16 {
	S := make([]int, p.npar+1)
	S[0] = -1
	for i := 0; i < p.npar; i++ {
		if syndrome[i] == 0 {
			S[i+1] = -1
		} else {
			exp := int(Galois16.logTbl[syndrome[i]]) + p.npar*i
			S[i+1] = (exp & Galois16.max) + (exp >> Galois16.w)
		}
	}
	modSyn := Galois16.gfconv(p.erasureLocPol, S, p.npar+1)
	omega := modSyn
	tsiDiff := p.erasureDiff
	ePlaces := p.erasuresPos

	par := make([]uint16, p.npar)
	for ii := 0; ii < len(ePlaces); ii++ {
		point := Galois16.max - ePlaces[ii]
		errDen := Galois16.gfsubstitute(tsiDiff, point, len(tsiDiff))
		errNum := Galois16.gfsubstitute(omega, point, len(omega))
		pow := errNum + ePlaces[ii] + ii + Galois16.max - errDen
		if errNum == -1 {
			par[p.npar-1-ii] = 0
		} else {
			par[p.npar-1-ii] = Galois16.expTbl[(pow&Galois16.max)+(pow>>Galois16.w)]
		}
	}
	return par
}

// Syndrome2ParityBytes converts full syndrome matrix to parity bytes (stride x npar).
func Syndrome2ParityBytes(syndrome [][]uint16) []byte {
	if len(syndrome) == 0 {
		return nil
	}
	npar := len(syndrome[0])
	out := make([]byte, len(syndrome)*npar*2)
	converter := NewParityToSyndrome(npar)
	for y := 0; y < len(syndrome); y++ {
		parRow := converter.SyndromeRowToParity(syndrome[y])
		for i, v := range parRow {
			base := y*npar*2 + i*2
			out[base] = byte(v)
			out[base+1] = byte(v >> 8)
		}
	}
	return out
}
