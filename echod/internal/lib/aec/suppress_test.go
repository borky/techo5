package aec

import (
	"math"
	"math/rand"
	"testing"
)

// suppressRun feeds the suppressor n samples of what the canceller was given and gave back, 320 at a
// time as the capture does, and returns everything it gave back.
func suppressRun(s *Suppressor, in, out []int16) []int16 {
	var got []int16
	for at := 0; at+320 <= len(in); at += 320 {
		got = append(got, s.Process(in[at:at+320], out[at:at+320])...)
	}
	return got
}

func rms(x []int16) float64 {
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(x)))
}

// echoAndLeak is white noise standing in for music, as the microphone heard it (in) and as the filter
// left a tenth of it (out).
func echoAndLeak(n int, seed int64) (in, out []int16) {
	r := rand.New(rand.NewSource(seed))
	in, out = make([]int16, n), make([]int16, n)
	for i := range in {
		e := r.NormFloat64() * 3000
		in[i], out[i] = int16(e), int16(e/10)
	}
	return in, out
}

// Where the filter took nothing out there is no echo to suppress: what comes back is what went in,
// SuppressorDelay samples late.
func TestSuppressorPassesWhatHoldsNoEcho(t *testing.T) {
	s := NewSuppressor(2, -10)
	r := rand.New(rand.NewSource(1))
	x := make([]int16, 16000)
	for i := range x {
		x[i] = int16(r.NormFloat64() * 3000)
	}
	got := suppressRun(s, x, x)
	if len(got) != len(x) {
		t.Fatalf("%d samples back for %d", len(got), len(x))
	}
	worst := 0.0
	for i := SuppressorDelay; i < len(got); i++ {
		worst = max(worst, math.Abs(float64(got[i])-float64(x[i-SuppressorDelay])))
	}
	if worst > 2 {
		t.Errorf("a sample differs by %.0f from what went in", worst)
	}
}

// Echo the filter left, with nobody talking, is taken down to the floor once the leak is learned.
func TestSuppressorTakesDownTheLeftoverEcho(t *testing.T) {
	s := NewSuppressor(2, -10)
	in, out := echoAndLeak(4*16000, 2)
	got := suppressRun(s, in, out)
	before, after := rms(out[3*16000:]), rms(got[3*16000:])
	if db := 20 * math.Log10(after/before); db > -8 {
		t.Errorf("the leftover echo came down %.1f dB, want about the floor's 10", -db)
	}
}

// A voice over the echo, in bins the echo barely reaches past the filter, comes through: a tone well
// over the leftover echo keeps nearly all of itself.
func TestSuppressorKeepsAVoiceOverTheEcho(t *testing.T) {
	s := NewSuppressor(2, -10)
	in, out := voiceOver(5*16000, 3*16000, 3)
	got := suppressRun(s, in, out)
	if db := 20 * math.Log10(toneAmp(got, 4*16000, 5*16000)/2000); db < -1.5 {
		t.Errorf("the voice came through %.1f dB down", -db)
	}
}

// voiceOver is echoAndLeak with a 1 kHz tone, a voice, over all of it from sample from on.
func voiceOver(n, from int, seed int64) (in, out []int16) {
	in, out = echoAndLeak(n, seed)
	for i := from; i < n; i++ {
		v := int16(2000 * math.Sin(2*math.Pi*1000*float64(i)/16000))
		in[i] += v
		out[i] += v
	}
	return in, out
}

// toneAmp is the 1 kHz tone's amplitude in got over [from, to), allowing for the delay.
func toneAmp(got []int16, from, to int) float64 {
	var c, q float64
	for i := from; i < to; i++ {
		at := float64(i - SuppressorDelay)
		c += float64(got[i]) * math.Cos(2*math.Pi*1000*at/16000)
		q += float64(got[i]) * math.Sin(2*math.Pi*1000*at/16000)
	}
	return 2 * math.Hypot(c, q) / float64(to-from)
}

// Restart drops the audio in flight and keeps the leak, so a voice in the first moments of the next
// run comes through; Reset forgets it, and until it is learned again the predicted echo is all taken
// for leak, the voice with it.
func TestSuppressorRestartKeepsTheLeak(t *testing.T) {
	s := NewSuppressor(2, -10)
	in, out := echoAndLeak(4*16000, 4)
	suppressRun(s, in, out)

	// The first 100 ms of a run with a voice in it from the start.
	in, out = voiceOver(16000/2, 0, 5)
	from, to := SuppressorDelay, SuppressorDelay+1600
	s.Restart()
	kept := toneAmp(suppressRun(s, in, out), from, to)
	s.Reset()
	forgot := toneAmp(suppressRun(s, in, out), from, to)
	if db := 20 * math.Log10(kept/2000); db < -2 {
		t.Errorf("after Restart the voice came through %.1f dB down at once, want what was learned kept", -db)
	}
	if forgot >= kept {
		t.Errorf("after Reset the voice came through at %.0f, after Restart at %.0f: Reset kept the leak", forgot, kept)
	}
}

// The capture's own goroutine runs this for every block while something plays: no garbage.
func TestSuppressorDoesNotAllocate(t *testing.T) {
	s := NewSuppressor(2, -10)
	in, out := echoAndLeak(320, 6)
	suppressRun(s, in, out) // the buffers' first growth
	if n := testing.AllocsPerRun(100, func() { s.Process(in, out) }); n != 0 {
		t.Errorf("%v allocations a block", n)
	}
}
