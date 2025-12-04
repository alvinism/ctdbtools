package parity

import "testing"

func TestMulDiv(t *testing.T) {
	g := Galois16
	if g.mul(3, 5) == 0 {
		t.Fatalf("expected non-zero mul")
	}
	x := g.mul(123, 456)
	y := g.div(x, 456)
	if y != 123 {
		t.Fatalf("div inverse failed: got %d", y)
	}
}

func TestEncodeTable(t *testing.T) {
	tab := Galois16.makeEncodeTable(4)
	if len(tab) != 256 {
		t.Fatalf("unexpected outer len %d", len(tab))
	}
}
