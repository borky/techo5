package media

import (
	"math"
)

// rateConv brings mono speech from one rate to another through a low pass at the lower rate's
// Nyquist, for Voice Quality's rates: speech that arrives at another rate than the one asked for, a
// reply Home Assistant streams at the pipeline's 16 kHz, or speech going into a call, which runs at
// 16 kHz whatever the room hears. The pipeline's own rate keeps the straight-line toVoiceRate it
// always had, so a device left at the standard quality plays exactly what it did.
//
// Straight lines between samples are no filter at all on the way down: 24 kHz speech taken to 16 kHz
// that way folds what was between 8 and 12 kHz back under 8 kHz, as grit on the consonants. This is a
// windowed sinc, read from a table rather than worked out for every tap, and it keeps what it needs of
// the input between calls, so speech converted piece by piece comes out as one continuous signal.
type rateConv struct {
	from, to int
	step     float64 // input samples per output sample
	scale    float64 // the low pass as a fraction of the input's Nyquist
	half     int     // how many input samples each side of an output sample the filter reaches
	buf      []float32
	pos      float64 // where the next output sample falls in buf
}

const (
	// convZeros is how many of the sinc's zero crossings each side are kept, and convOver how many
	// table points there are between two: 16 crossings under a Blackman window is about 74 dB down
	// on what it removes.
	convZeros = 16
	convOver  = 128

	// convDown and convUp are the cutoff against the lower rate's Nyquist. Going down it sits a
	// little under, so the transition band ends before anything can fold back; going up there is
	// nothing to fold, and nothing above the input's Nyquist to keep.
	convDown = 0.9
	convUp   = 0.95
)

// convTable is the windowed sinc at convOver points per zero crossing, from the center out.
var convTable = func() []float32 {
	n := convZeros*convOver + 1
	t := make([]float32, n+1)
	for i := range n {
		x := float64(i) / convOver
		s := 1.0
		if x != 0 {
			s = math.Sin(math.Pi*x) / (math.Pi * x)
		}
		// Blackman, over the whole span: one at the center, nothing at the last zero crossing.
		w := 0.42 + 0.5*math.Cos(math.Pi*x/convZeros) + 0.08*math.Cos(2*math.Pi*x/convZeros)
		t[i] = float32(s * w)
	}
	return t // one past the end left at zero, for the interpolation at the last point
}()

func newRateConv(from, to int) *rateConv {
	// A rate of nothing would never step forward through the input; such a conversion is none.
	if from <= 0 || to <= 0 {
		from, to = 1, 1
	}
	c := &rateConv{from: from, to: to, step: float64(from) / float64(to)}
	if to < from {
		c.scale = convDown * float64(to) / float64(from)
	} else {
		c.scale = convUp
	}
	c.half = int(math.Ceil(convZeros / c.scale))
	// History of silence, so the first output sample lines up with the first input sample.
	c.buf = make([]float32, c.half)
	c.pos = float64(c.half)
	return c
}

// kernel is the low pass at x input samples from its center, scaled for the cutoff.
func (c *rateConv) kernel(x float64) float32 {
	t := math.Abs(x) * c.scale * convOver
	i := int(t)
	if i >= convZeros*convOver {
		return 0
	}
	f := float32(t - float64(i))
	return float32(c.scale) * (convTable[i] + (convTable[i+1]-convTable[i])*f)
}

// Run converts the next piece of speech, holding back the samples the filter still needs to see past.
func (c *rateConv) Run(in []int16) []int16 {
	for _, s := range in {
		c.buf = append(c.buf, float32(s))
	}
	var out []int16
	for c.pos+float64(c.half) < float64(len(c.buf)) {
		center := int(c.pos)
		var acc float32
		for k := center - c.half + 1; k <= center+c.half; k++ {
			acc += c.buf[k] * c.kernel(c.pos-float64(k))
		}
		out = append(out, clampSample(acc))
		c.pos += c.step
	}
	// Keep only what the next output sample can still reach.
	if keep := int(c.pos) - c.half; keep > 0 {
		c.buf = append(c.buf[:0], c.buf[keep:]...)
		c.pos -= float64(keep)
	}
	return out
}

// Flush converts what Run held back, as speech that ends here.
func (c *rateConv) Flush() []int16 {
	return c.Run(make([]int16, c.half))
}

// convertRate is a whole clip of mono speech at another rate, its length the clip's at that rate.
func convertRate(in []int16, from, to int) []int16 {
	if from == to || from <= 0 || to <= 0 || len(in) == 0 {
		return in
	}
	c := newRateConv(from, to)
	out := append(c.Run(in), c.Flush()...)
	if want := int(int64(len(in)) * int64(to) / int64(from)); len(out) > want {
		out = out[:want]
	}
	return out
}

// clampSample is a sample held to 16 bits. The filter overshoots a transient by a little, and only
// speech already at full scale reaches past it; the speech's own gain and limiter come after.
func clampSample(v float32) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}
