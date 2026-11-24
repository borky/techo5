package media

import (
	"context"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// AnnounceBuffer is how much of an announcement is collected before any of it plays (#559). A text to
// speech engine that streams sentence by sentence is slower than speech for the first words, and this
// is enough to ride over that without making a fast engine wait noticeably longer than it used to.
const AnnounceBuffer = 750 * time.Millisecond

// SpeechResume is how much to hold after a dip, rather than the whole buffer again: rebuilding it
// every time turns a shortfall of a few milliseconds into a third of a second of silence.
const SpeechResume = 80 * time.Millisecond

// VoiceOut is where streamed speech goes: the speaker's player, or a test's stand-in.
type VoiceOut interface {
	PlayVoice(mono []int16)
	Queued() int
}

// voiceAtOut is a VoiceOut that also plays speech at Voice Quality's higher rates (the speaker's
// player does). Speech at such a rate handed to one that does not is brought down to the pipeline's.
type voiceAtOut interface {
	PlayVoiceAt(mono []int16, rate int)
}

// play hands the stream's speech to p: at its rate to a speaker that plays it there, and brought down
// to the pipeline's through one converter for the whole stream to one that does not, so the pieces join.
func (s *SpeechStream) play(p VoiceOut, mono []int16) {
	r := s.rate()
	if r == speaker.VoiceRate {
		p.PlayVoice(mono)
		return
	}
	if at, ok := p.(voiceAtOut); ok {
		at.PlayVoiceAt(mono, r)
		return
	}
	if s.toPipeline == nil {
		s.toPipeline = newRateConv(r, speaker.VoiceRate)
	}
	p.PlayVoice(s.toPipeline.Run(mono))
}

// SpeechStream is speech arriving in pieces, played as it goes through a cushion: Buffer of it is held
// before anything plays, and after the queue runs dry SpeechResume is held before playing again. It is
// what a streamed reply and a streamed announcement both play through. One is used for one piece of
// speech, from one goroutine; the zero value plays with no cushion, no gain and nothing else.
type SpeechStream struct {
	// Buffer is how much to collect before playing any of it.
	Buffer time.Duration

	// Gain brings the speech toward the speech loudness target, by its loudness so far. The first
	// flush waits for a buffer's worth, so it already has something to measure, and later flushes
	// move the gain only as the speech's level changes.
	Gain bool

	// Louder runs Louder speech's compressor, kept across flushes, when that switch is on.
	Louder bool

	// Say, when set, is handed each flush as well, with the gain but not the compressor: a call
	// that is up hears the speech as the room does.
	Say func([]int16)

	// Started is told once, just before the first audio is queued.
	Started func()

	// Rate is the rate the speech arrives at: zero is the pipeline's, speaker.VoiceRate, and speech
	// fetched at Voice Quality's rate says so (SpeechRate). What goes into a call through Say is at the
	// pipeline's rate whatever this is.
	Rate int
	// toCall brings speech at Rate down to the call's, piece by piece, where they differ, and
	// toPipeline down to the speaker's where the speaker plays only the pipeline's rate.
	toCall, toPipeline *rateConv
	// SayWanted, when set, says whether a call wants the speech just now, so speech at a higher rate
	// is brought down to the call's only while one does.
	SayWanted func() bool

	// held is audio waiting for the cushion to fill, and flowing says the cushion is built and audio
	// is going straight through.
	held    []int16
	flowing bool

	louder Louder

	// energy and samples are the loudness so far, for the gain.
	energy  float64
	samples int

	bytes     int
	peak      int
	playingAt time.Time
}

// Take is a piece of the speech: 16-bit mono samples at the stream's rate in little-endian bytes. It
// reports whether anything was queued, which is not until the cushion has filled.
func (s *SpeechStream) Take(data []byte, p VoiceOut) bool {
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		s.peak = max(s.peak, int(samples[i]), -int(samples[i]))
	}
	s.bytes += len(data)
	s.energy += SumSquares(samples)
	s.samples += len(samples)

	s.held = append(s.held, samples...)
	if p.Queued() == 0 {
		s.flowing = false
	}

	want := s.Buffer
	if !s.playingAt.IsZero() {
		want = SpeechResume
	}
	if !s.flowing && len(s.held) < samplesAt(want, s.rate()) {
		return false
	}

	if s.playingAt.IsZero() {
		s.playingAt = time.Now()
	}
	s.flowing = true
	s.flush(p)
	return true
}

// Finish plays whatever is still held, which will never be joined by more, and what the converters
// were holding back to see past it.
func (s *SpeechStream) Finish(p VoiceOut) {
	s.flush(p)
	if s.toCall != nil && s.Say != nil {
		if tail := s.toCall.Flush(); len(tail) > 0 {
			s.Say(tail)
		}
		s.toCall = nil
	}
	if s.toPipeline != nil {
		if tail := s.toPipeline.Flush(); len(tail) > 0 {
			p.PlayVoice(tail)
		}
		s.toPipeline = nil
	}
}

func (s *SpeechStream) flush(p VoiceOut) {
	out := s.held
	s.held = nil
	if len(out) == 0 {
		return
	}
	if s.Gain {
		if gain := SpeechGain(s.energy, s.samples); gain > 1 {
			out = Scale(out, gain)
		}
	}
	if s.Started != nil {
		s.Started()
		s.Started = nil
	}
	if s.Say != nil {
		if r := s.rate(); r == speaker.VoiceRate {
			s.Say(out)
		} else if s.SayWanted == nil || s.SayWanted() {
			if s.toCall == nil {
				s.toCall = newRateConv(r, speaker.VoiceRate)
			}
			s.Say(s.toCall.Run(out))
		}
	}
	if s.Louder {
		s.louder.Rate = s.rate()
		out = ForSpeaker(out, &s.louder)
	}
	s.play(p, out)
}

// rate is the rate the speech is at.
func (s *SpeechStream) rate() int {
	if s.Rate <= 0 {
		return speaker.VoiceRate
	}
	return s.Rate
}

// Bytes is how much speech has arrived, Peak the loudest sample of it as it arrived, and PlayingAt
// when it began to play (zero until the cushion first filled).
func (s *SpeechStream) Bytes() int           { return s.bytes }
func (s *SpeechStream) Peak() int            { return s.peak }
func (s *SpeechStream) PlayingAt() time.Time { return s.playingAt }

// samplesAt is a duration as a count of samples at rate.
func samplesAt(d time.Duration, rate int) int {
	return int(d/time.Millisecond) * rate / 1000
}

// PlaySpeech fetches speech Home Assistant is serving and plays it through s as it arrives, which is
// what an announcement does (#559): an engine that makes speech sentence by sentence is heard from its
// first sentence rather than after its last. A WAVE body plays as it comes; anything else (MP3) is
// read whole and decoded first, as it always was. The tail of silence follows what played.
//
// Waiting for it to be heard is the driver's: this returns once the last of it has been queued. One
// that fails before any audio fails; one that fails part way, or stalls, has played what came and ends
// there. One canceled returns the context's error, so whatever was to follow it is not started.
func PlaySpeech(ctx context.Context, url string, s *SpeechStream, p VoiceOut) error {
	err := fetchVoice(ctx, url, speechStall, s.rate(), func(data []byte) { s.Take(data, p) })
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.Finish(p)
	if s.bytes > 0 {
		s.play(p, make([]int16, s.rate()*Tail/1000))
	}
	if err != nil && s.bytes > 0 {
		slog.Warn("the announcement ended early", "err", err, "bytes", s.bytes)
		return nil
	}
	if err == nil {
		slog.Info("announcement played", "samples", s.bytes/2, "peak", s.peak)
	}
	return err
}
