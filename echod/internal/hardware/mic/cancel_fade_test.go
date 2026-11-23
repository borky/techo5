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
