package parity

import "testing"

func TestMulDiv(t *testing.T) {
	g := GF16
	if g.Mul(3, 5) == 0 {
		t.Fatalf("expected non-zero mul")
	}
	x := g.Mul(123, 456)
	y := g.Div(x, 456)
	if y != 123 {
		t.Fatalf("div inverse failed: got %d", y)
	}
}

func TestEncodeTable(t *testing.T) {
	tab := EncodeTable(4)
	if len(tab) != 256*4*2 {
		t.Fatalf("unexpected table len %d", len(tab))
	}
}
