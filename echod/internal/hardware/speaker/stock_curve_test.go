package speaker

import (
	"math"
	"testing"
)

// The Show's stock curve passes through every measured step, rises all the way, is silent at 0 and
// reaches the tuning's full input at 30; a set nobody measured has none.
func TestStockCurve(t *testing.T) {
	c, ok := stockCurve("show")
	if !ok {
		t.Fatal("the Show has no stock curve")
	}
	for _, pt := range stockPoints["show"] {
		if got := c[int(pt[0])]; math.Abs(got-pt[1]) > 1e-9 {
			t.Errorf("step %v: %.2f dB, measured %.2f", pt[0], got, pt[1])
		}
	}
	for s := 1; s <= VolumeSteps; s++ {
		if c[s] <= c[s-1] {
			t.Errorf("step %d (%.2f dB) is not above step %d (%.2f dB)", s, c[s], s-1, c[s-1])
		}
	}
	if c[0] != mute || c[VolumeSteps] != 0 {
		t.Errorf("ends at %.1f and %.1f dB", c[0], c[VolumeSteps])
	}
	// Between two measured steps the line is straight: step 13 is a third of the way from 12 to 15.
	if want := -24.8 + (-19.7+24.8)/3; math.Abs(c[13]-want) > 1e-9 {
		t.Errorf("step 13: %.3f dB, want %.3f", c[13], want)
	}
	for _, set := range []string{"dot", "crown", "spot", "radar"} {
		if _, ok := stockCurve(set); ok {
			t.Errorf("%s has a stock curve nobody measured", set)
		}
	}
}

// The switch picks the stock curve over the usual one, and over Louder top volume's, only where the
// tuning has one.
func TestStockCurvePicksTheCurve(t *testing.T) {
	first := [VolumeSteps + 1]float64{-90}
	lifted := liftTop(first, 8)
	stock, _ := stockCurve("show")
	p := &Player{first: &first, lifted: &lifted, stock: &stock}
	if p.inFront() != &first {
		t.Error("off, the curve is not the usual one")
	}
	p.SetStockCurve(true)
	if p.inFront() != &stock {
		t.Error("on, the curve is not the stock one")
	}
	p.SetLouderTop(true)
	if p.inFront() != &stock {
		t.Error("with Louder top volume on as well, the curve is not the stock one")
	}
	p.stock = nil
	if p.inFront() != &lifted {
		t.Error("on a tuning with no stock curve, the switch changed the curve")
	}
	if (&Player{}).StockCurveAvailable() || !(&Player{stock: &stock}).StockCurveAvailable() {
		t.Error("available does not follow whether there is a stock curve")
	}
}

// With the switch on, a step goes into the tuning hotter than firstCurves' from step 2 up, which is
// what it is for: as loud as Fire OS at the same step. Step 1 is the exception, as it was under Fire
// OS, where it was under a room's noise.
func TestStockCurveIsHotterThanTheUsualOne(t *testing.T) {
	first, ok := firstCurves["show"]
	if !ok {
		t.Skip("this build has no Show curve")
	}
	stock, _ := stockCurve("show")
	for s := 2; s <= VolumeSteps; s++ {
		if stock[s] <= first[s] {
			t.Errorf("step %d: stock %.1f dB is not above the usual %.1f", s, stock[s], first[s])
		}
	}
}
