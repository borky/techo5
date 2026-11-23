package mic

import (
	"math"
	"testing"
)

// The switches into the suppressor's late output and back out of it fade rather than step: a run's
// first block starts where the microphone is and fades into the suppressor's silence, and the block a
// run ends on is the microphone again, faded in from what was still in flight.
func TestCancellerFadesAtTheSwitches(t *testing.T) {
	c := newCanceller()
	c.suppressing = true
	mic := make([]int16, FrameSamples)
	for i := range mic {
		mic[i] = int16(4000 * math.Sin(2*math.Pi*300*float64(i)/Rate))
	}

	first := c.apply(rawFrame(0, true), mic)
	if first == nil {
		t.Fatal("a playing block was not canceled")
	}
	// Sample 100, well into the fade: the microphone, a little faded.
	if want := float64(mic[100]) * (1 - 100.0/switchFade); first[0] != mic[0] || math.Abs(float64(first[100])-want) > 3 {
		t.Errorf("a run's first block starts at %d and is %d at sample 100, want the microphone's %d and about %.0f",
			first[0], first[100], mic[0], want)
	}
	for i := switchFade; i < len(first); i++ {
		if first[i] < -2 || first[i] > 2 {
			t.Fatalf("sample %d of the first block is %d: past the fade it is the suppressor's silence", i, first[i])
		}
	}

	// Play on, then stop, and wait out the hold.
	for n := 1; n < 20; n++ {
		c.apply(rawFrame(n, true), mic)
	}
	var last []int16
	for n := 20; n < 20+Rate/FrameSamples+3; n++ {
		if out := c.apply(rawFrame(n, false), mic); out != nil {
			last = out
		}
	}
	if c.active.Load() {
		t.Fatal("the run did not end")
	}
	for i := switchFade; i < len(last); i++ {
		if last[i] != mic[i] {
			t.Fatalf("sample %d of the run's last block is %d, want the microphone's %d", i, last[i], mic[i])
		}
	}
	if !c.starting {
		t.Error("after the run the suppressor was not set to fade into the next one")
	}
}

// Off, which is the default, the suppressor is not in the path: a run's first block is the filter's
// output from its first sample. Turned off in the middle of a run, the next block fades from what it
// still held into the filter's output, and the one after is the filter's alone.
func TestSuppressorOffIsTheFilterAlone(t *testing.T) {
	src := &Source{cancel: newCanceller()}
	c := src.cancel
	if src.Suppressing() {
		t.Fatal("the suppressor is on by default")
	}
	mic := make([]int16, FrameSamples)
	for i := range mic {
		mic[i] = int16(4000 * math.Sin(2*math.Pi*300*float64(i)/Rate))
	}
	first := c.apply(rawFrame(0, true), mic)
	if first == nil || first[switchFade+10] == 0 {
		t.Fatal("with the suppressor off, a run's first block is silence past the fade: it ran")
	}

	src.SetSuppressing(true)
	for n := 1; n < 20; n++ {
		c.apply(rawFrame(n, true), mic)
	}
	src.SetSuppressing(false)
	if !c.stopping {
		t.Fatal("turned off during a run, it was not set to fade out")
	}
	c.apply(rawFrame(20, true), mic)
	if c.stopping {
		t.Error("the fade out did not happen on the next block")
	}
	// The block after is the filter's own output, undelayed: the same as a canceller that never
	// suppressed, fed the same blocks.
	plain := newCanceller()
	for n := 0; n <= 21; n++ {
		want := plain.apply(rawFrame(n, true), mic)
		if n == 21 {
			got := c.apply(rawFrame(n, true), mic)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample %d after turning it off is %d, the filter alone gives %d", i, got[i], want[i])
				}
			}
		}
	}
}
