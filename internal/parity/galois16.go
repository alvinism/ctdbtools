package parity

// Galois16 implements GF(2^16) arithmetic with polynomial 0x1100b (matching CUETools Galois16).
type Galois16 struct {
	Exp [65536]int
	Log [65536]int
	Max int
}

var GF16 = newGalois16()

func newGalois16() *Galois16 {
	g := &Galois16{Max: 65535}
	const poly = 0x1100b
	x := 1
	for i := 0; i < g.Max; i++ {
		g.Exp[i] = x
		g.Log[x] = i
		x <<= 1
		if x&0x10000 != 0 {
			x ^= poly
		}
	}
	for i := g.Max; i < len(g.Exp); i++ {
		g.Exp[i] = g.Exp[i-g.Max]
	}
	g.Log[0] = -1
	return g
}

func (g *Galois16) Mul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return g.Exp[(g.Log[a]+g.Log[b])%g.Max]
}

func (g *Galois16) Div(a, b int) int {
	if a == 0 {
		return 0
	}
	if b == 0 {
		return 0
	}
	return g.Exp[(g.Log[a]-g.Log[b]+g.Max)%g.Max]
}

func (g *Galois16) MulExp(a int, n int) int {
	if a == 0 {
		return 0
	}
	return g.Exp[(g.Log[a]+n)%g.Max]
}

func (g *Galois16) DivExp(a int, n int) int {
	if a == 0 {
		return 0
	}
	return g.Exp[(g.Log[a]-n+g.Max)%g.Max]
}
