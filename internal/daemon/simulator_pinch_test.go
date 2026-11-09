package daemon

import (
	"math"
	"testing"
)

func TestPinchPaths(t *testing.T) {
	if pinchPaths(100, 100, 1, 0) != nil || pinchPaths(100, 100, 0, 0) != nil {
		t.Fatal("a scale of 1 or 0 isn't a pinch")
	}
	gap := func(p [][]struct{ X, Y, T float64 }, i int) float64 {
		return math.Hypot(p[1][i].X-p[0][i].X, p[1][i].Y-p[0][i].Y)
	}
	for _, scale := range []float64{2, 0.5} {
		p := pinchPaths(200, 300, scale, 500)
		if len(p) != 2 || len(p[0]) != len(p[1]) {
			t.Fatalf("scale %v: want two fingers with matching samples, got %d", scale, len(p))
		}
		last := len(p[0]) - 1
		if got := gap(p, last) / gap(p, 0); math.Abs(got-scale) > 1e-9 {
			t.Errorf("scale %v: the fingers' gap changed by %v", scale, got)
		}
		if p[0][last].T != 500 {
			t.Errorf("scale %v: ends at %vms, want 500", scale, p[0][last].T)
		}
		// Centred on the point throughout.
		if mx := (p[0][last].X + p[1][last].X) / 2; mx != 200 {
			t.Errorf("scale %v: centre drifted to x=%v", scale, mx)
		}
		// Never closer than 2×28pt: the fingers stay distinct.
		if gap(p, 0) < 50 || gap(p, last) < 50 {
			t.Errorf("scale %v: fingers too close (%v, %v)", scale, gap(p, 0), gap(p, last))
		}
	}
}
