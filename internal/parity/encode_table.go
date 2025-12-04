package parity

// EncodeTable builds the syndrome encode table used by CUETools (makeEncodeTable).
// npar is the number of parity bytes (max 16).
func EncodeTable(npar int) []uint16 {
	tab := make([]uint16, 256*npar*2)
	g := GF16
	for i := 0; i < 256; i++ {
		for j := 0; j < npar; j++ {
			tab[(i*npar+j)*2] = uint16(g.MulExp(i, j))
			tab[(i*npar+j)*2+1] = uint16(g.MulExp(i, (j+npar)%g.Max))
		}
	}
	return tab
}
