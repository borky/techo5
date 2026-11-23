package mic

import (
	"log/slog"
	"math"
	"sync/atomic"

	"github.com/HuskerMinion/techo5/echod/internal/lib/aec"
	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

// cancelTaps is how far the filter reaches, in samples, so 64 ms of tail. Not a setting: measured on
// this enclosure, cancellation rises with every tap available — 16.5 dB at 256, 18.6 at 512, 23.0 at
// 1024, 27.2 at 2048 — so there is no room-dependent best value to look for, only what the device can
// afford. This costs 6.4% of a core, and only while something is playing.
const cancelTaps = 1024

// cancelTapsJack is the filter's reach while the sound leaves by the headphone jack, 256 ms. A
// powered speaker on the jack sits across the room, not a few centimeters from the microphones, and
// its echo comes back late and spread out: measured on a Dot driving a portable speaker over AUX
// (2026-11-15, white noise, the seven microphones averaged), the path peaks at 22-33 ms and holds 90%
// of its energy by 88 ms and 99% by 185 ms. An ideal filter takes 7 dB off it at 1024 taps, 13 at
// 2048 and 19 at 4096, while a fixed delay in front of 1024 taps buys only 1.5 dB: the filter was too
// short, not too late. The capture replayed through this filter, six seconds of noise, gave 8.6, 14.5
// and 15.4 dB, the last still rising. On the device, replies and sounds had 6-8 dB taken off before,
// and a song 20.8 after. The filter's cost grows with its length, so this is about four times
// cancelTaps' 6.4% of a core, and only while the jack plays, on the builds where longJack is set.
const cancelTapsJack = 4096

// LongEchoPath reports whether playback currently leaves by a path whose echo needs cancelTapsJack:
// the speaker package sets it to "the output is the headphone jack". Nil, as in tests and tools that
// never start a speaker, keeps cancelTaps. Read when a run starts, never during one.
var LongEchoPath func() bool

// tapsWanted is the filter length for the run about to start.
func tapsWanted() int {
	if longJack && LongEchoPath != nil && LongEchoPath() {
		return cancelTapsJack
	}
	return cancelTaps
}

// cancelMu is how fast the filter adapts. It still takes 10 dB off the music within half a second of a
// reply, and keeps what it learned between replies, so the first second is not where it matters.
//
// Faster leaves less music between words but costs the voice over it. Measured on 127 wake word takes
// laid over a Dot's recorded music at 10 dB under its echo: 36 caught at 0.5, 72 at 0.1, 74 at 0.05,
// 66 at 0.02, with 2 dB more music left at 0.1 than at 0.5. Pausing the learning while somebody talks
// did not help (35 caught), which points at the fast learning itself bending the voice rather than at
// what it learns from it.
const cancelMu = 0.1

// suppressOver and suppressFloorDB set the residual echo suppressor after the built-in filter
// (aec.Suppressor), where Microphone echo suppression turns it on (SetSuppressing): twice the leftover echo it estimates is taken off, and no bin loses more than
// 10 dB. Measured on an Echo Show 5 1st gen with loud music and the wake word said over it every five
// seconds for two minutes, replayed through the daemon's own path: the filter alone heard it 5 times,
// with the suppressor 7 or 8 (a change as small as its delay moves one either way), most scores rising
// from 0.4-0.7 to over 0.9; 30 s of the same music alone scored nothing near a wake word either way.
// A floor of -15 dB heard no more and took more off a voice; once over the estimate heard fewer.
const (
	suppressOver    = 2
	suppressFloorDB = -10
)

// refQuiet is the mean square per sample, at int16 scale, below which the loopback counts as silence.
// About -76 dBFS. Below it there is no echo to remove, so the filter is skipped entirely and the frame
// costs nothing — which is what keeps this free on an idle device. It has to sit well under a
// radio at a quiet volume: measured at -50 dBFS on the loopback, which a -60 dBFS gate flapped on
// every second, resetting the canceller each time.
const refQuiet = 1e-7 * 32768 * 32768 / 4

// refHold is how long the filter keeps running after the loopback goes quiet, in samples.
//
// Speech is full of gaps, and without this the gate flaps between every word. That is not only untidy:
// the room is still ringing with the echo of the word that just played, and the measured tail here runs
// past 100 ms, so disengaging on the gap stops canceling exactly as that tail arrives. A second
// covers it with room to spare, and rides across the pauses in a radio talk show.
const refHold = Rate

// canceller subtracts the playback loopback from one fixed acoustic path, and replaces the mix while it
// runs.
//
// The path it reads cannot move: the filter learns one path from the speaker, and the beamformer steers
// at the loudest sound, which during playback is our own speaker. Steering into the echo is the opposite
// of useful when the echo is what is being removed. A fixed combination of microphones is as fixed as one
// microphone, though, because the microphones do not move: on the Dot, the average of seven canceled
// once measured exactly as well as seven cancellers averaged, and 5 dB better than the center microphone
// alone. Which path it reads is cancelInput's choice.
type canceller struct {
	// filter is swapped by fitTaps from the capture loop while SetAdapting reaches it from elsewhere.
	filter atomic.Pointer[aec.Canceller]

	// adapting is what SetAdapting last asked for, so a filter built by fitTaps starts the same way.
	adapting atomic.Bool

	// ext is WebRTC's canceller in a helper process (webrtc.go), used instead of filter while it is
	// set and healthy. engine names which one the user asked for.
	ext    *external
	engine string

	// ref and mono are reused every frame; decoding into them keeps the audio path free of allocation.
	ref  []int16
	mono []int16

	// erle is published for diagnostics, as thousandths of a dB so it fits an integer. best is the most
	// it reached during the current run: ERLE is averaged over the last half second, so reading it as
	// playback ends catches the reference fading out and says the filter did worse than it did.
	erle atomic.Int64
	best atomic.Int64

	// active says whether the last frame had playback in it, which is when any of this happened.
	active atomic.Bool

	// hold counts down the samples still to filter after the loopback went quiet. Reader-only.
	hold int

	// sup takes down what the built-in filter leaves (suppressOver). WebRTC's canceller has its own.
	// Its output is SuppressorDelay (40 ms) late, so a run starting puts that much silence into what
	// the microphone hands on, and a run ending drops the 40 ms still in flight: a wake chime that
	// starts a run while someone goes on talking leaves a 40 ms pause in their words. The switches
	// fade over switchFade rather than step, and every start restarts its audio (restartSup) and
	// keeps what it learned of the leak, as the filter keeps its path. Reader-only.
	sup *aec.Suppressor
	// starting says the next block out of sup is the first since it restarted, to fade into.
	starting bool
	// suppressing is whether sup runs at all (SetSuppressing), and stopping that it was just turned
	// off, so the next block fades from what it still held into the filter's own output.
	suppressing, stopping bool
}

// switchFade is how long, in samples, the switches into and out of the suppressor's late output fade
// across: 10 ms, which takes the step out without smearing a word.
const switchFade = Rate / 100

// restartSup starts the suppressor's audio over, for a run starting or the engine changing under one:
// what it held belongs to another stretch, or to the other engine's.
func (c *canceller) restartSup() {
	c.sup.Restart()
	c.starting = true
}

// fade writes into dst, over its first switchFade samples, a fade from from to to; past that, to.
func fade(dst, from, to []int16) {
	n := min(len(dst), len(from), len(to))
	copy(dst, to[:n])
	for i := range min(n, switchFade) {
		r := float32(i) / switchFade
		dst[i] = int16(float32(from[i])*(1-r) + float32(to[i])*r)
	}
}

func newCanceller() *canceller {
	f, err := aec.New(aec.Config{Taps: cancelTaps, Mu: cancelMu})
	if err != nil {
		// Only a bad Taps or Mu reaches this, and both are constants above.
		slog.Error("echo cancellation unavailable", "err", err)
		return nil
	}
	c := &canceller{engine: "builtin", sup: aec.NewSuppressor(suppressOver, suppressFloorDB), starting: true}
	c.filter.Store(f)
	c.adapting.Store(f.Adapting())
	return c
}

// fitTaps rebuilds the filter at taps long when it is another length, keeping whether it learns. What
// it had learned goes: a new length comes with a new output, and the path it knew is not this one.
// Called only as a run starts, so no block is ever canceled by half a filter.
func (c *canceller) fitTaps(taps int) {
	old := c.filter.Load()
	if old.Taps() == taps {
		return
	}
	f, err := aec.New(aec.Config{Taps: taps, Mu: cancelMu})
	if err != nil {
		slog.Error("echo cancellation: keeping the filter as it is", "taps", taps, "err", err)
		return
	}
	f.SetAdapting(c.adapting.Load())
	c.filter.Store(f)
	f.SetAdapting(c.adapting.Load()) // a SetAdapting between the two lines above reached the old filter
	slog.Info("echo cancellation filter length", "from", old.Taps(), "to", taps)
}

// setEngine picks the canceller: "builtin" is the LMS filter above, "webrtc" the helper process.
// It reports what it settled on: webrtc falls back to builtin when the helper cannot start.
func (c *canceller) setEngine(name string) string {
	c.restartSup()
	if c.ext != nil {
		c.ext.Close()
		c.ext = nil
	}
	c.engine = "builtin"
	if name != "webrtc" {
		return c.engine
	}
	e, err := startExternal("--ns", "low")
	if err != nil {
		slog.Warn("webrtc echo cancellation unavailable, using the built-in filter", "err", err)
		return c.engine
	}
	c.ext, c.engine = e, "webrtc"
	return c.engine
}

// process runs one block through whichever engine is on. A helper that has died is dropped for
// the built-in filter, once, with a log line.
func (c *canceller) process(mic, ref []int16) ([]int16, float64, error) {
	if c.ext != nil {
		if c.ext.Healthy() {
			out, err := c.ext.Process(mic, ref)
			if err == nil {
				return out, c.ext.ERLE(), nil
			}
			slog.Warn("webrtc echo cancellation failed, using the built-in filter", "err", err)
		}
		c.ext.Close()
		c.ext = nil
		c.engine = "builtin"
		c.restartSup()
	}
	f := c.filter.Load()
	out, err := f.Process(mic, ref)
	if err != nil {
		return nil, 0, err
	}
	return out, f.ERLE(), nil
}

// apply returns mic, one fixed path (see cancelInput), with the echo removed, or nil when there is
// nothing playing and the caller should use the mix it already has. The block a run ends on is mic,
// faded in from what the suppressor still held (switchFade).
func (c *canceller) apply(raw []byte, mic []int16) []int16 {
	n := len(mic)
	if cap(c.ref) < n {
		c.ref = make([]int16, n)
	}
	c.ref = c.ref[:n]
	referenceInto(raw, c.ref)

	switch {
	case playing(c.ref):
		c.hold = refHold
	case c.hold > 0:
		// Still inside the tail of what just played.
		c.hold -= n
	default:
		// Nothing to cancel. The filter keeps what it learned: the room has not changed just because
		// the reply ended, so the next one starts converged rather than from nothing.
		if c.active.Swap(false) {
			slog.Info("echo cancellation idle",
				"best_db", float64(c.best.Load())/1000, "last_db", float64(c.erle.Load())/1000)
			c.erle.Store(0)
			c.best.Store(0)
			if c.ext == nil && c.suppressing {
				// The first block back on the plain path fades in from what the suppressor still
				// held, rather than stepping from 40 ms ago to now.
				c.mono = append(c.mono[:0], mic...)
				fade(c.mono, c.sup.InFlight(), mic)
				c.restartSup()
				return c.mono
			}
			c.restartSup()
		}
		return nil
	}

	if !c.active.Swap(true) {
		c.fitTaps(tapsWanted())
		attrs := []any{"engine", c.engine, "ref_dbfs", blockDBFS(c.ref), "mic_dbfs", blockDBFS(mic)}
		if c.engine == "builtin" {
			attrs = append(attrs, "taps", c.filter.Load().Taps()) // WebRTC's canceller has its own
		}
		slog.Info("echo cancellation running", attrs...)
	}

	out, erleDB, err := c.process(mic, c.ref)
	if err != nil {
		slog.Error("echo cancellation failed", "err", err)
		return nil
	}
	if c.ext == nil && c.stopping {
		c.stopping = false
		fade(out, c.sup.InFlight(), out)
		c.restartSup()
	}
	if c.ext == nil && c.suppressing {
		out = c.sup.Process(mic, out)
		if c.starting {
			// The suppressor's first block is silence: fade into it from the microphone as it is now,
			// which carries on from the plain path the block before.
			c.starting = false
			fade(out, mic, out)
		}
	}
	erle := int64(erleDB * 1000)
	c.erle.Store(erle)
	if erle > c.best.Load() {
		c.best.Store(erle)
	}

	// The engines hand back a buffer of their own that the next block overwrites, and the caller runs
	// the denoiser and the leveler over what this returns, so the block is moved somewhere they may
	// write on. This buffer is reused as well: what apply returns is only good until the next call,
	// and broadcast copies it before any listener keeps it.
	if cap(c.mono) < len(out) {
		c.mono = make([]int16, len(out))
	}
	c.mono = c.mono[:len(out)]
	copy(c.mono, out)
	return c.mono
}

// blockDBFS is the RMS of a block in dBFS, rounded, for the log.
func blockDBFS(s []int16) float64 {
	if len(s) == 0 {
		return -120
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	rms := math.Sqrt(sum / float64(len(s)))
	if rms < 1 {
		return -120
	}
	return math.Round(20*math.Log10(rms/32768)*10) / 10
}

// playing reports whether the loopback carries anything.
func playing(ref []int16) bool {
	var sum float64
	for _, v := range ref {
		sum += float64(v) * float64(v)
	}
	return len(ref) > 0 && sum/float64(len(ref)) > refQuiet
}

// referenceInto decodes ch7, the left half of the playback loopback, into dst. ch8 is left alone: on
// music the two measure within 12-17 dB of each other, which caps cancellation far above anything the
// filter reaches, so a stereo reference would buy nothing.
func referenceInto(raw []byte, dst []int16) {
	frameBytes := Channels * Bits / 8

	frames := min(len(raw)/frameBytes, len(dst))
	for f := range frames {
		o := f*frameBytes + RefFirst*3
		dst[f] = int16(audio.DecodeS24LE3(raw[o:o+3]) >> 8)
	}
	for f := frames; f < len(dst); f++ {
		dst[f] = 0
	}
}

// Canceling reports whether the canceller is currently running, which it does only while the loopback
// is carrying audio.
func (s *Source) Canceling() bool {
	return s.cancel != nil && s.cancel.active.Load()
}

// ERLE is how much echo the canceller is removing, in dB, or zero when it is not running.
func (s *Source) ERLE() float64 {
	if s.cancel == nil {
		return 0
	}
	return float64(s.cancel.erle.Load()) / 1000
}

// SetCanceling turns echo cancellation on or off. Off is the same signal path the device had before it
// existed, which is what makes it worth having as a switch: it is the comparison.
func (s *Source) SetCanceling(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceling = on
}

// SetSuppressing turns the residual echo suppressor after the built-in canceller on or off
// (aec.Suppressor). Off, the default, is the canceller's output as it was before the suppressor
// existed. Either way the switch fades, as a run starting or ending does.
func (s *Source) SetSuppressing(on bool) {
	if s.cancel == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cancel
	switch {
	case on && !c.suppressing:
		c.restartSup()
	case !on && c.suppressing:
		c.stopping = c.active.Load()
	}
	c.suppressing = on
}

// Suppressing is whether the residual echo suppressor is on.
func (s *Source) Suppressing() bool {
	if s.cancel == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel.suppressing
}

// SetCancelEngine chooses which canceller runs — "builtin" or "webrtc" — and reports what it
// settled on. Serialized with the capture loop, since the engine is used from there.
func (s *Source) SetCancelEngine(name string) string {
	if s.cancel == nil {
		return "builtin"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel.setEngine(name)
}

// CancelEngine is which canceller is running.
func (s *Source) CancelEngine() string {
	if s.cancel == nil {
		return "builtin"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel.engine
}

// SetAdapting stops or resumes the canceller learning, while it goes on canceling with what it has.
//
// Turn it off while somebody is being listened to. The filter cannot tell a voice it was never given a
// reference for from an echo it predicted badly, so it treats the voice as its own error and fits itself
// to it — at exactly the moment canceling matters. What it already learned stays correct: the room did
// not change because somebody spoke.
func (s *Source) SetAdapting(on bool) {
	if s.cancel != nil {
		s.cancel.adapting.Store(on)
		s.cancel.filter.Load().SetAdapting(on)
	}
}
