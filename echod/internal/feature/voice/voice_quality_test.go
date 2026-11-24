package voice

import (
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// A reply streamed over the API is at the pipeline's rate; one fetched at a Voice Quality is at that
// rate, and how long it plays is counted at it.
func TestAReplysRate(t *testing.T) {
	streamed := newStream(0, 0, 0)
	if streamed.rate() != speaker.VoiceRate {
		t.Fatalf("a streamed reply is at %d Hz", streamed.rate())
	}
	fetched := newStream(0, 0, 0)
	fetched.speech.Rate = 48000
	if fetched.rate() != 48000 {
		t.Fatalf("a fetched reply is at %d Hz", fetched.rate())
	}
	if got := replyAudioSecondsAt(96000, fetched.rate()); got != 1 {
		t.Fatalf("a second of 48 kHz audio counted as %.2f s", got)
	}
}

// At the standard quality the device offers what it always did; above it, everything but Speaker, so
// Home Assistant makes replies in the announcement format. Streamed delivery is fetched early only on a
// connection offered no speaker, and only for a reply played here.
func TestVoiceQualityDecidesSpeakerAndDelivery(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { unstreamed.Store(false) })

	if Offer() != Features {
		t.Fatalf("standard offer %b, want %b", Offer(), Features)
	}
	_ = config.Set().Wake(0).Delivery(config.DeliveryStream)
	if got := deliveryFor(0); got != config.DeliveryStream {
		t.Errorf("offered the speaker: delivery %q, want streamed", got)
	}

	_ = config.Set().Speaker().VoiceQuality(config.VoiceHigh)
	if got := Offer(); got != FeaturesFor(true) {
		t.Fatalf("high quality offer %b, want %b", got, FeaturesFor(true))
	}
	if got := deliveryFor(0); got != config.DeliveryEarly {
		t.Errorf("offered no speaker: delivery %q, want early", got)
	}
	_ = config.Set().Brain().SetReplyPlayer("media_player.kitchen")
	if got := deliveryFor(0); got != config.DeliveryStream {
		t.Errorf("with a reply player: delivery %q, want the slot's own", got)
	}
	_ = config.Set().Brain().SetReplyPlayer("")

	_ = config.Set().Wake(0).Delivery(config.DeliveryWhole)
	if got := deliveryFor(0); got != config.DeliveryWhole {
		t.Errorf("whole file became %q", got)
	}
}

// At High, on a connection offered no speaker, a slot set to Streamed has its reply fetched early: the
// early offer is taken, a stream starting does not claim the turn for a stream that is not coming, and
// the url at the end of the speech is the same reply, not fetched a second time.
func TestAStreamedSlotAtHighIsFetchedEarlyOnce(t *testing.T) {
	c := thinkingTurn(t)
	if err := config.Set().Wake(0).Delivery(config.DeliveryStream); err != nil {
		t.Fatal(err)
	}
	if wakeword.Delivery(0) != config.DeliveryStream {
		t.Skip("could not set the delivery")
	}
	if err := config.Set().Speaker().VoiceQuality(config.VoiceHigh); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unstreamed.Store(false) })
	if Offer()&esphome.FeatureSpeaker != 0 {
		t.Fatal("at High the speaker was offered")
	}
	url, release := heldSpeech(t)

	c.runURL = url
	c.handle(event{kind: evEarlyReply})
	if !c.reply.early || c.reply.url != url {
		t.Fatalf("the early offer was not taken: early %v, url %q", c.reply.early, c.reply.url)
	}
	took := c.replies
	c.handle(event{kind: evStreamStart})
	c.handle(event{kind: evReplyURL, url: url})
	if c.replies != took || c.reply.url != url {
		t.Fatalf("the reply was started again (%d replies, want %d; url %q)", c.replies, took, c.reply.url)
	}
	close(release)
	next(t, c, evPlayed)
}
