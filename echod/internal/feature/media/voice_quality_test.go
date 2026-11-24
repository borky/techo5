package media

import (
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// atOut is a speaker that also plays Voice Quality's rates, and keeps each piece with its rate.
type atOut struct {
	mu    sync.Mutex
	rates []int
	got   []int16
}

func (a *atOut) PlayVoice(mono []int16) { a.PlayVoiceAt(mono, speaker.VoiceRate) }
func (a *atOut) PlayVoiceAt(mono []int16, rate int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rates = append(a.rates, rate)
	a.got = append(a.got, mono...)
}
func (a *atOut) Queued() int { return 0 }

// Speech at 48 kHz plays at 48 kHz on a speaker that can, and goes into a call at the call's 16 kHz:
// the same speech, a third of the samples, the same tone.
func TestSpeechAtAHigherRatePlaysThereAndCallsGetSixteen(t *testing.T) {
	in := tone(1000, 48000, 48000/2, 8000)
	var said []int16
	s := &SpeechStream{Rate: 48000, Say: func(out []int16) { said = append(said, out...) }}
	out := &atOut{}
	for i := 0; i < len(in); i += 1536 {
		s.Take(pcmBytes(in[i:min(i+1536, len(in))]), out)
	}
	s.Finish(out)

	if len(out.got) != len(in) {
		t.Fatalf("played %d samples of %d", len(out.got), len(in))
	}
	for _, r := range out.rates {
		if r != 48000 {
			t.Fatalf("played at %d Hz, want 48000", r)
		}
	}
	if want := len(in) / 3; len(said) < want-200 || len(said) > want {
		t.Fatalf("%d samples into the call, want about %d", len(said), want)
	}
	if db := 20 * math.Log10(level(said)/level(tone(1000, 16000, len(said), 8000))); math.Abs(db) > 0.3 {
		t.Errorf("the call's tone is %.2f dB off", db)
	}
}

// A speaker that knows only the pipeline's rate is handed speech at that rate, not played fast.
func TestSpeechAtAHigherRateOnAPipelineSpeaker(t *testing.T) {
	in := tone(1000, 24000, 2400, 8000)
	out := newFakeOut(0)
	s := &SpeechStream{Rate: 24000}
	s.Take(pcmBytes(in), out)
	s.Finish(out)
	got, _ := out.all()
	if want := len(in) * 2 / 3; len(got) < want-2 || len(got) > want {
		t.Fatalf("%d samples, want %d at 16 kHz", len(got), want)
	}
}

// Fetched at a Voice Quality, a body at that rate passes untouched and one at another rate arrives at
// it: three times the samples from a 16 kHz body asked for at 48 kHz.
func TestFetchVoiceAtARate(t *testing.T) {
	for _, c := range []struct{ body, want int }{{48000, 48000}, {16000, 48000}, {24000, 24000}, {16000, 24000}} {
		in := tone(500, c.body, c.body/10, 6000)
		body := append(wavHeader(1, c.body), pcmBytes(in)...)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))

		var got []byte
		if err := FetchVoiceAt(context.Background(), srv.URL, c.want, func(b []byte) { got = append(got, b...) }); err != nil {
			t.Fatal(err)
		}
		srv.Close()
		n, want := len(got)/2, len(in)*c.want/c.body
		if n < want-2 || n > want+40 {
			t.Errorf("%d Hz asked at %d: %d samples, want %d", c.body, c.want, n, want)
		}
		if c.body == c.want {
			for i := range in {
				if v := int16(binary.LittleEndian.Uint16(got[i*2:])); v != in[i] {
					t.Fatalf("%d Hz at its own rate: sample %d is %d, want %d untouched", c.body, i, v, in[i])
				}
			}
		}
	}
}

// Home Assistant is offered announcements at the chosen rate, music as before, and the standard
// formats are the ones the device always offered.
func TestFormatsFollowVoiceQuality(t *testing.T) {
	if Formats[1].SampleRate != speaker.VoiceRate || !Formats[1].Announcement {
		t.Fatalf("standard announcements at %d Hz", Formats[1].SampleRate)
	}
	f := formatsFor(48000)
	if f[0] != Formats[0] {
		t.Errorf("music changed: %+v", f[0])
	}
	if f[1].SampleRate != 48000 || f[1].Channels != 1 || !f[1].Announcement {
		t.Errorf("announcements offered as %+v", f[1])
	}
}

// Louder speech follows a word at the same speed in time whatever the rate: the same burst, compressed
// at 16, 24 and 48 kHz, comes out at the same level, to within what the 16 kHz coefficients at a
// higher rate would miss by (about 0.16 dB at 24 kHz, 0.32 at 48).
func TestLouderIsTheSameAtEveryRate(t *testing.T) {
	burst := func(rate int) []int16 {
		s := tone(400, rate, rate/2, 9000)
		for i := range s[:rate/4] {
			s[i] /= 6 // a quiet start, so the level has something to follow
		}
		return s
	}
	ref := level((&Louder{}).Process(burst(16000)))
	for _, rate := range []int{24000, 48000} {
		got := level((&Louder{Rate: rate}).Process(burst(rate)))
		if db := 20 * math.Log10(got/ref); math.Abs(db) > 0.05 {
			t.Errorf("%d Hz: %.3f dB from the 16 kHz level", rate, db)
		}
	}
}

// Choosing a quality asks for a reconnect once the device is idle, and leaves the formats alone while a
// connection could be reading them; they follow the saved quality between two spells of serving
// (describeFormats). Choosing the same rate again asks for nothing.
func TestVoiceQualityReachesTheOfferedFormats(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := &Player{mp: &esphome.MediaPlayer{SupportedFormats: Formats}, quality: &esphome.Select{}}
	asked := 0
	defer component.ReconnectWhenIdle.Listen(func(struct{}) { asked++ })()

	p.setVoiceQuality(config.VoiceStandard)
	if asked != 0 {
		t.Fatalf("the same rate asked for %d reconnects", asked)
	}
	p.setVoiceQuality(config.VoiceHighest)
	if asked != 1 {
		t.Fatalf("%d reconnects asked for, want 1", asked)
	}
	if p.mp.SupportedFormats[1].SampleRate != speaker.VoiceRate {
		t.Fatal("the formats changed while a connection could be reading them")
	}
	if err := config.Set().Speaker().VoiceQuality(config.VoiceHighest); err != nil {
		t.Fatal(err)
	}
	p.describeFormats(config.Get())
	if got := p.mp.SupportedFormats[1].SampleRate; got != 48000 {
		t.Fatalf("announcements offered at %d Hz after the reconnect, want 48000", got)
	}

	// With replies on another player the quality changes neither, and asks for no reconnect where the
	// device can hear (a device that cannot keeps its own formats, so it still does).
	if err := config.Set().Brain().SetReplyPlayer("media_player.kitchen"); err != nil {
		t.Fatal(err)
	}
	asked = 0
	p.setVoiceQuality(config.VoiceHigh)
	if want := map[bool]int{true: 0, false: 1}[layout.HasMicrophone()]; asked != want {
		t.Errorf("with a reply player, %d reconnects asked for, want %d", asked, want)
	}
}

// A WAVE header that says no rate, or an impossible one, is refused, not stretched forever.
func TestABodyWithNoRateIsRefused(t *testing.T) {
	for _, rate := range []int{0, 1, 1 << 30} {
		body := append(wavHeader(1, rate), make([]byte, 4096)...)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
		done := make(chan error, 1)
		go func() { done <- FetchVoiceAt(context.Background(), srv.URL, 24000, func([]byte) {}) }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%d Hz was taken", rate)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%d Hz: still converting after 3 s", rate)
		}
		srv.Close()
	}
}

// A body arriving in many small reads converts to the same speech as one read all at once.
func TestFetchVoiceAtAcrossReads(t *testing.T) {
	in := tone(700, 16000, 8000, 9000)
	body := append(wavHeader(1, 16000), pcmBytes(in)...)
	fetch := func(chunk int) []byte {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f := w.(http.Flusher)
			for i := 0; i < len(body); i += chunk {
				_, _ = w.Write(body[i:min(i+chunk, len(body))])
				f.Flush()
			}
		}))
		defer srv.Close()
		var got []byte
		if err := FetchVoiceAt(context.Background(), srv.URL, 48000, func(b []byte) { got = append(got, b...) }); err != nil {
			t.Fatal(err)
		}
		return got
	}
	whole, pieces := fetch(len(body)), fetch(97)
	if len(whole) != len(pieces) {
		t.Fatalf("%d bytes in pieces, %d whole", len(pieces), len(whole))
	}
	for i := range whole {
		if whole[i] != pieces[i] {
			t.Fatalf("byte %d differs", i)
		}
	}
}
