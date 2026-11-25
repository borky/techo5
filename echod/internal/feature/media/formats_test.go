package media

import (
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func announcement(t *testing.T, fs []esphome.MediaFormat) esphome.MediaFormat {
	t.Helper()
	for _, f := range fs {
		if f.Announcement {
			return f
		}
	}
	t.Fatal("no announcement format")
	return esphome.MediaFormat{}
}

// Replies sent to another player are asked for at the card's full rate, mono (#715), whatever Voice
// Quality is; replies here are at Voice Quality's rate, the voice rate unless it is set higher. Music is
// the same either way.
func TestFormatsForWhereRepliesPlay(t *testing.T) {
	here, elsewhere := FormatsFor(false, speaker.VoiceRate), FormatsFor(true, speaker.VoiceRate)
	if len(here) != len(Formats) || &here[0] != &Formats[0] {
		t.Fatal("replies here do not ask for the usual formats")
	}
	if a := announcement(t, here); a.SampleRate != speaker.VoiceRate || a.Channels != 1 {
		t.Errorf("replies here: announcements at %d Hz, %d channels", a.SampleRate, a.Channels)
	}
	a := announcement(t, elsewhere)
	if a.Format != "wav" || a.SampleRate != speaker.Rate || a.Channels != 1 || a.SampleBytes != 2 {
		t.Errorf("replies elsewhere: announcements as %+v, want %d Hz mono 16-bit WAV", a, speaker.Rate)
	}
	if elsewhere[0] != Formats[0] || elsewhere[0].Announcement {
		t.Errorf("music changed with the reply player: %+v", elsewhere[0])
	}
	if a := announcement(t, FormatsFor(false, 24000)); a.SampleRate != 24000 || a.Channels != 1 {
		t.Errorf("replies here at High: announcements at %d Hz, %d channels", a.SampleRate, a.Channels)
	}
	if a := announcement(t, FormatsFor(true, 24000)); a.SampleRate != speaker.Rate {
		t.Errorf("replies elsewhere at High: announcements at %d Hz, want %d", a.SampleRate, speaker.Rate)
	}
	if got := FormatsFor(false, 24000)[0]; got != Formats[0] {
		t.Errorf("music changed with Voice Quality: %+v", got)
	}
}

// The player takes its formats from the setting, and a device that cannot hear keeps its own whatever
// the setting says.
func TestThePlayerDescribesItsFormats(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := &Player{mp: &esphome.MediaPlayer{}}
	p.describeFormats(config.Get())
	if announcement(t, p.mp.SupportedFormats).SampleRate != speaker.VoiceRate {
		t.Fatal("with no player the announcement rate is not the voice rate")
	}
	if err := config.Set().Speaker().VoiceQuality(config.VoiceHigh); err != nil {
		t.Fatal(err)
	}
	p.describeFormats(config.Get())
	if got := announcement(t, p.mp.SupportedFormats).SampleRate; got != uint32(config.VoiceHigh.Rate()) {
		t.Fatalf("at High with no player the announcement rate is %d", got)
	}
	if err := config.Set().Brain().SetReplyPlayer("media_player.kitchen"); err != nil {
		t.Fatal(err)
	}
	p.describeFormats(config.Get())
	want := uint32(speaker.VoiceRate)
	if layout.HasMicrophone() {
		want = uint32(speaker.Rate)
	}
	if got := announcement(t, p.mp.SupportedFormats).SampleRate; got != want {
		t.Fatalf("with a player the announcement rate is %d, want %d", got, want)
	}
}
