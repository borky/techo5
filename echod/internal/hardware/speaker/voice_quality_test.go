package speaker

import (
	"math"
	"testing"
)

// Every Voice Quality plays the same speech at the same level: a 1 kHz tone comes out of the 24 and
// 48 kHz paths as loud as out of the pipeline's 16 kHz one, so choosing a higher quality never sounds
// better only by being louder.
func TestVoiceQualitiesPlayAtOneLevel(t *testing.T) {
	var s sinc
	ref := energyAt(left(s.Run(sine(1000, VoiceRate, 4000, 8000), nil))[512:], 1000, Rate)
	for _, rate := range []int{24000, 48000} {
		u := newUpsampler(Rate / rate)
		in := sine(1000, rate, 4000*rate/VoiceRate, 8000)
		out := left(u.Run(in, nil))
		if len(out) != len(in)*Rate/rate {
			t.Errorf("%d Hz: %d samples out of %d, want %d", rate, len(out), len(in), len(in)*Rate/rate)
		}
		got := energyAt(out[512:], 1000, Rate)
		if db := 20 * math.Log10(got/ref); math.Abs(db) > 0.3 {
			t.Errorf("%d Hz: the tone is %.2f dB from the 16 kHz path's", rate, db)
		}
	}
}

// At 24 kHz the image of a 5 kHz tone, at 19 kHz, is filtered as the 16 kHz path filters its own, and
// both channels carry the same signal.
func TestTheTwentyFourKilohertzPathRejectsImages(t *testing.T) {
	u := newUpsampler(2)
	out := u.Run(sine(5000, 24000, 4096, 8000), nil)
	for i := 0; i < len(out); i += Channels {
		if out[i] != out[i+1] {
			t.Fatalf("frame %d: %d and %d", i/Channels, out[i], out[i+1])
		}
	}
	l := left(out)[512:]
	tone, image := energyAt(l, 5000, Rate), energyAt(l, 19000, Rate)
	if image > tone/100 {
		t.Errorf("the image at 19 kHz is %.2f, within 40 dB of the tone at %.2f", image, tone)
	}
}

// Speech at 24 kHz handed over in pieces comes out as the same signal as handed over whole: the
// upsampler keeps its history between calls, as the 16 kHz one does, and Drain starts it again.
func TestVoiceAtRateIsContinuousAcrossPieces(t *testing.T) {
	in := sine(700, 24000, 2400, 9000)
	whole := (&Player{}).voiceAtRate(in, 24000)

	p := &Player{}
	var pieces []int16
	for i := 0; i < len(in); i += 333 {
		pieces = append(pieces, p.voiceAtRate(in[i:min(i+333, len(in))], 24000)...)
	}
	if len(pieces) != len(whole) {
		t.Fatalf("%d samples in pieces, %d whole", len(pieces), len(whole))
	}
	for i := range whole {
		if pieces[i] != whole[i] {
			t.Fatalf("sample %d: %d in pieces, %d whole", i, pieces[i], whole[i])
		}
	}

	p.voice = &sinc{}
	p.Drain()
	again := p.voiceAtRate(in[:333], 24000)
	for i := range again {
		if again[i] != whole[i] {
			t.Fatalf("after Drain, sample %d is %d, want %d as from the start", i, again[i], whole[i])
		}
	}
}

// A rate the codec's is not a whole multiple of is not played at the wrong speed: nothing is.
func TestAnOddRateIsNotPlayed(t *testing.T) {
	if out := (&Player{}).voiceAtRate(sine(500, 22050, 2205, 8000), 22050); len(out) != 0 {
		t.Fatalf("%d samples played", len(out))
	}
}
