package media

import (
	"math"
	"testing"
)

func tone(hz float64, rate, n int, amp float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return out
}

// level is the RMS of the middle half of s, away from the filter's start and end.
func level(s []int16) float64 {
	m := s[len(s)/4 : 3*len(s)/4]
	var sum float64
	for _, v := range m {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(m)))
}

// A tone in the band comes through every conversion Voice Quality makes at its own level, and at the
// right length for the new rate.
func TestRateConvKeepsTheBand(t *testing.T) {
	for _, c := range []struct{ from, to int }{
		{16000, 24000}, {16000, 48000}, {24000, 48000}, {48000, 16000}, {24000, 16000},
		{22050, 24000}, {44100, 48000}, {44100, 24000}, {22050, 16000},
	} {
		in := tone(1000, c.from, c.from/2, 10000)
		out := convertRate(in, c.from, c.to)
		if want := len(in) * c.to / c.from; len(out) < want-1 || len(out) > want {
			t.Errorf("%d to %d: %d samples, want %d", c.from, c.to, len(out), want)
		}
		ref := level(tone(1000, c.to, len(out), 10000))
		if got := level(out); math.Abs(20*math.Log10(got/ref)) > 0.2 {
			t.Errorf("%d to %d: a 1 kHz tone came out %.2f dB off", c.from, c.to, 20*math.Log10(got/ref))
		}
	}
}

// Going down, what the lower rate cannot carry is taken out rather than folded back under it: a 10 kHz
// tone at 24 or 48 kHz is gone at 16 kHz, where straight lines would leave it as a 6 kHz whistle.
func TestRateConvDoesNotFoldBack(t *testing.T) {
	for _, from := range []int{24000, 48000, 44100} {
		in := tone(10000, from, from/2, 10000)
		out := convertRate(in, from, 16000)
		if got := 20 * math.Log10(level(out)/level(in)); got > -60 {
			t.Errorf("%d to 16000: a 10 kHz tone is only %.1f dB down", from, got)
		}
		old := toVoiceRate(in, from)
		t.Logf("%d to 16000: a 10 kHz tone %.1f dB down here, %.1f dB with straight lines", from,
			20*math.Log10(level(out)/level(in)), 20*math.Log10(level(old)/level(in)))
	}
}

// Speech converted piece by piece is the same signal as speech converted whole: nothing is lost or
// doubled at the joins, whatever size the pieces come in.
func TestRateConvInPiecesIsTheWhole(t *testing.T) {
	for _, c := range []struct{ from, to int }{{16000, 48000}, {16000, 24000}, {48000, 16000}, {22050, 24000}} {
		in := tone(440, c.from, c.from/4, 12000)
		whole := newRateConv(c.from, c.to)
		want := append(whole.Run(in), whole.Flush()...)

		pieces := newRateConv(c.from, c.to)
		var got []int16
		for i, size := 0, 1; i < len(in); size = size%997 + 13 {
			end := min(i+size, len(in))
			got = append(got, pieces.Run(in[i:end])...)
			i = end
		}
		got = append(got, pieces.Flush()...)
		if len(got) != len(want) {
			t.Fatalf("%d to %d: %d samples in pieces, %d whole", c.from, c.to, len(got), len(want))
		}
		for i := range got {
			if d := int(got[i]) - int(want[i]); d > 1 || d < -1 {
				t.Fatalf("%d to %d: sample %d is %d in pieces and %d whole", c.from, c.to, i, got[i], want[i])
			}
		}
	}
}
