package parity

// SyndromeCalc mirrors CUETools' ParityToSyndrome.Parity2Syndrome for a single stride.
// Given parity bytes, stride, and npar, produce syndrome matrix [stride][npar].
func ParityToSyndrome(stride int, npar int, parity []byte) [][]uint16 {
	g := GF16
	syn := make([][]uint16, stride)
	for i := 0; i < stride; i++ {
		syn[i] = make([]uint16, npar)
	}
	for i := 0; i < stride; i++ {
		for j := 0; j < npar; j++ {
			var v int
			for k := 0; k < npar; k++ {
				p := int(parity[i*npar+k])
				if p == 0 {
					continue
				}
				v ^= g.MulExp(p, (j*k)%g.Max)
			}
			syn[i][j] = uint16(v)
		}
	}
	return syn
}
