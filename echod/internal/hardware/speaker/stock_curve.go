package speaker

// Stock volume curve: the stock firmware's own volume curve in front of the tuning, in place of
// firstCurves'.
//
// firstCurves were made to keep each step as loud as it was with the volume behind the tuning, and on
// the Show 5 1st gen that tops out at -19.5 dB: step 30 there is about as loud as step 15 under Fire
// OS. The chain behind it is the same one (a test signal through Fire OS's audio HAL, dumped digitally
// before and after its processing, comes out of lib/asp within half a decibel), so what differs is how
// hot it is fed. Fed that much cooler, the same loudness takes a higher step, and a higher step is a
// flatter EQ bucket: the quiet buckets' extra bass and treble, the loudness compensation the tuning
// exists for, is gone by the time the speaker is as loud as it was under Fire OS. With this switch the
// steps are Fire OS's, so a step sounds as it did under the stock firmware: as loud, through the same
// bucket. The top steps are then far louder than firstCurves', and the compressor and limiter work as
// hard as they did under Fire OS. Off by default, so nothing changes unless someone asks for it.

// stockPoints are each tuning set's stock curve where it has been measured, as (step, dB in front of
// the tuning) pairs, rising; stockCurve fills the steps between them in.
//
// "show" is the Show 5 1st gen's, under Fire OS: the music stream's index stepped 30..0 and back under
// a steady tone, recorded from beside it, 470, 1070 and 2230 Hz within 0.5 dB of each other at nearly
// every index (so no equalizer that follows the volume ahead of the tuning). Index 1 was under the
// room's noise: it is carried on at the slope of the two above it. Index 12 checked digitally: a test
// signal reached the audio HAL 25.6 dB down, against 24.8 here. The 2nd gen reads its curve off its
// own AFE.cfg (ownVolumeTable), so this is used only where that is not.
var stockPoints = map[string][][2]float64{
	"show": {
		{0, mute}, {1, -68.8}, {2, -60}, {3, -51.2}, {4, -47.2}, {5, -43.7}, {6, -41}, {9, -31.8},
		{12, -24.8}, {15, -19.7}, {18, -15.8}, {21, -12.3}, {24, -7.6}, {27, -4}, {30, 0},
	},
}

// stockCurve is the stock curve for a tuning set, every step filled in along straight lines between
// the measured ones, or false where the set has none.
func stockCurve(set string) ([VolumeSteps + 1]float64, bool) {
	var c [VolumeSteps + 1]float64
	pts, ok := stockPoints[set]
	if !ok || len(pts) < 2 {
		return c, false
	}
	j := 0
	for s := 0; s <= VolumeSteps; s++ {
		for j < len(pts)-2 && float64(s) > pts[j+1][0] {
			j++
		}
		a, b := pts[j], pts[j+1]
		c[s] = a[1] + (b[1]-a[1])*(float64(s)-a[0])/(b[0]-a[0])
	}
	return c, true
}

// StockCurveAvailable is whether this device plays a tuning the switch has a curve for: false untuned,
// on a tuning that has none, and where the curve in front already is the unit's own.
func (p *Player) StockCurveAvailable() bool { return p.stock != nil }

// SetStockCurve turns Stock volume curve on or off. The write loop picks the curve up with the next
// period, so the level changes at once; where there is no stock curve it does nothing.
func (p *Player) SetStockCurve(on bool) { p.stockOn.Store(on) }
