package parity

// Galois implements GF(2^w) arithmetic with log/exp tables.
// This ports CUETools.Parity.Galois (subset needed for parity/syndrome work).
type Galois struct {
	expTbl   []uint16
	logTbl   []uint16
	w        int
	max      int
	symStart int
}

// NewGalois constructs a field with the given primitive polynomial and width.
func NewGalois(polynomial int, w int) *Galois {
	max := (1 << w) - 1
	expTbl := make([]uint16, max*2)
	logTbl := make([]uint16, max+1)
	d := 1
	for i := 0; i < max; i++ {
		expTbl[i] = uint16(d)
		expTbl[max+i] = uint16(d)
		logTbl[d] = uint16(i)
		d <<= 1
		if ((d >> w) & 1) != 0 {
			d = (d ^ polynomial) & max
		}
	}
	return &Galois{expTbl: expTbl, logTbl: logTbl, w: w, max: max}
}

var Galois16 = NewGalois(0x1100B, 16)

func (g *Galois) Max() int         { return g.max }
func (g *Galois) Width() int       { return g.w }
func (g *Galois) ExpTbl() []uint16 { return g.expTbl }
func (g *Galois) LogTbl() []uint16 { return g.logTbl }
func (g *Galois) toExp(a int) int  { return int(g.expTbl[a]) }
func (g *Galois) toLog(a int) int  { return int(g.logTbl[a]) }
func (g *Galois) MaxVal() int      { return g.max }
func (g *Galois) toPos(length, a int) int {
	return length - 1 - g.toLog(a)
}

func (g *Galois) mul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return int(g.expTbl[int(g.logTbl[a])+int(g.logTbl[b])])
}

func (g *Galois) mulExp(a, b int) int {
	if a == 0 {
		return 0
	}
	return int(g.expTbl[int(g.logTbl[a])+b])
}

func (g *Galois) div(a, b int) int {
	if a == 0 {
		return 0
	}
	return int(g.expTbl[int(g.logTbl[a])-int(g.logTbl[b])+g.max])
}

func (g *Galois) divExp(a, b int) int {
	if a == 0 {
		return 0
	}
	return int(g.expTbl[int(g.logTbl[a])-b+g.max])
}

// Exported wrappers
func (g *Galois) MulExp(a, b int) int { return g.mulExp(a, b) }
func (g *Galois) DivExp(a, b int) int { return g.divExp(a, b) }

// gfconv multiplies polynomials represented in log form (-1 is -Inf).
func (g *Galois) gfconv(a, b []int, length int) []int {
	res := make([]int, length)
	for ia := 0; ia < len(a); ia++ {
		loga := a[ia]
		if loga != -1 {
			ib2 := len(b)
			if length-ia < ib2 {
				ib2 = length - ia
			}
			for ib := 0; ib < ib2; ib++ {
				logb := b[ib]
				if logb != -1 {
					res[ia+ib] ^= int(g.expTbl[loga+logb])
				}
			}
		}
	}
	for i := 0; i < length; i++ {
		if res[i] == 0 {
			res[i] = -1
		} else {
			res[i] = int(g.logTbl[res[i]])
		}
	}
	return res
}

func (g *Galois) gfdiff(a []int) []int {
	res := make([]int, len(a)-1)
	for i := 0; i < len(res); i++ {
		if i%2 == 0 {
			res[i] = a[i+1]
		} else {
			res[i] = -1
		}
	}
	return res
}

// gfsubstitute evaluates a log-domain polynomial at value (also log-domain).
// Returns log-domain result or -1 for zero.
func (g *Galois) gfsubstitute(polynomial []int, value int, terms int) int {
	sum := 0
	if value != -1 {
		for p := 0; p < terms; p++ {
			if polynomial[p] != -1 {
				pow := polynomial[p] + value*p
				sum ^= int(g.expTbl[(pow&g.max)+(pow>>g.w)])
			}
		}
	}
	if sum == 0 {
		return -1
	}
	return int(g.logTbl[sum])
}

func (g *Galois) makeEncodeGx(npar int) []int {
	encodeGx := make([]int, npar)
	encodeGx[npar-1] = 1
	for i, kou := 0, g.symStart; i < npar; i, kou = i+1, kou+1 {
		ex := g.toExp(kou)
		for j := 0; j < npar-1; j++ {
			encodeGx[j] = g.mul(encodeGx[j], ex) ^ encodeGx[j+1]
		}
		encodeGx[npar-1] = g.mul(encodeGx[npar-1], ex)
	}
	return encodeGx
}

func (g *Galois) makeEncodeGxLog(npar int) []int {
	encodeGx := g.makeEncodeGx(npar)
	for i := 0; i < npar; i++ {
		if encodeGx[i] == 0 {
			panic("0 in encodeGx")
		}
		encodeGx[i] = g.toLog(encodeGx[i])
	}
	return encodeGx
}

// MakeEncodeTable matches CUETools Galois.makeEncodeTable: parityTable[byte, hi/lo, i].
func (g *Galois) MakeEncodeTable(npar int) [][][]uint16 {
	loggx := g.makeEncodeGxLog(npar)
	parity := make([][][]uint16, 256)
	for i := 0; i < 256; i++ {
		parity[i] = make([][]uint16, 2)
		for j := 0; j < 2; j++ {
			parity[i][j] = make([]uint16, npar)
		}
	}
	for ib := 1; ib < 256; ib++ {
		logib0 := g.logTbl[ib]
		logib1 := g.logTbl[ib<<8]
		for i := 0; i < npar; i++ {
			parity[ib][0][i] = g.expTbl[int(logib0)+loggx[i]]
			parity[ib][1][i] = g.expTbl[int(logib1)+loggx[i]]
		}
	}
	return parity
}

// toLogSlice converts numeric coefficients to log domain (-1 for zero).
func (g *Galois) toLogSlice(vals []int) []int {
	out := make([]int, len(vals))
	for i, v := range vals {
		if v == 0 {
			out[i] = -1
		} else {
			out[i] = g.toLog(v)
		}
	}
	return out
}
