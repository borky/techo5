package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// A device saved before Voice Quality existed, or never set, is at the pipeline's 16 kHz, and saves
// nothing new until it is changed; each choice is found again by its label.
func TestVoiceQualityDefaultsToThePipelinesRate(t *testing.T) {
	var s Speaker
	if err := json.Unmarshal([]byte(`{"volume":15,"resampling":"sinc","asp":true}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.VoiceQuality.Rate() != 16000 || s.VoiceQuality.Label() != "Standard (16 kHz)" {
		t.Fatalf("an old file is at %d Hz, %q", s.VoiceQuality.Rate(), s.VoiceQuality.Label())
	}
	b, _ := json.Marshal(defaultSpeaker())
	if strings.Contains(string(b), "voice_quality") {
		t.Errorf("the default is saved: %s", b)
	}
	for _, q := range VoiceQualities() {
		if got, ok := ByLabel(VoiceQualities(), q.Label()); !ok || got != q {
			t.Errorf("%q is not found by its label", q.Label())
		}
	}
	if VoiceHigh.Rate() != 24000 || VoiceHighest.Rate() != 48000 || VoiceQuality("other").Rate() != 16000 {
		t.Error("the rates are not 24, 48 and 16 kHz for anything else")
	}
}
