package speaker

import "math"

// Voice arrives at 16 kHz and the codec only takes 48 kHz, so every input sample turns into three.
// Repeating it three times is the cheap way and it sounds like it: a held sample is a zero-order
// hold, whose spectrum keeps images of the speech band mirrored around the input rate, which lands
// them in 8 kHz to 16 kHz where they are plainly audible as grit on every consonant.
//
// Interpolating with a low pass at the input's Nyquist removes them. The filter runs polyphase: of
// the taps that would be applied to a zero-stuffed signal, only one in three is ever multiplied by
// a non-zero sample, so each output sample costs voiceTaps multiplies rather than three times that.
const (
	voicePhases = VoiceUpsample
	voiceTaps   = 12
	voiceLength = voicePhases * voiceTaps
)

var voiceFilter = func() [voiceLength]float32 {
	var h [voiceLength]float32
	// Cutoff is half the input rate expressed against the output rate, and the sinc's amplitude
	// already carries the gain that zero stuffing would otherwise lose.
	center := float64(voiceLength-1) / 2
	for i := range h {
		x := (float64(i) - center) / voicePhases
		s := 1.0
		if x != 0 {
			s = math.Sin(math.Pi*x) / (math.Pi * x)
		}
		// Hamming, which trades a little transition width for a stopband deep enough that the
		// images sit below the noise the 24-bit codec contributes anyway.
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(voiceLength-1))
		// Replies arrive at very nearly full scale and interpolation overshoots a transient by a
		// few percent, so the result would clip. The headroom costs less than a decibel, and the
		// volume control downstream makes even that moot.
		h[i] = float32(s * w * 0.9)
	}
	return h
}()

// sinc turns a 16 kHz mono stream into 48 kHz stereo. It keeps the tail of the previous call, so an
// utterance delivered in chunks comes out as one continuous signal rather than with a seam at every
// boundary.
type sinc struct {
	hist [voiceTaps]float32

	// clipped counts samples that came out past full scale. An interpolating filter overshoots a
	// transient by a few percent, so audio that arrives already near full scale clips here — and
	// because the volume is applied further along, that distortion survives being turned down.
	clipped uint64
}

func (u *sinc) Reset()          { u.hist = [voiceTaps]float32{} }
func (u *sinc) Clipped() uint64 { return u.clipped }

// Run appends the interleaved stereo result of mono to out.
func (u *sinc) Run(mono []int16, out []int16) []int16 {
	for _, s := range mono {
		copy(u.hist[:], u.hist[1:])
		u.hist[voiceTaps-1] = float32(s)

		for p := range voicePhases {
			var acc float32
			for k := range voiceTaps {
				// Newest sample against the phase's first tap.
				acc += voiceFilter[p+k*voicePhases] * u.hist[voiceTaps-1-k]
			}
			if acc > math.MaxInt16 || acc < math.MinInt16 {
				u.clipped++
			}
			v := clamp16(acc)
			out = append(out, v, v)
		}
	}
	return out
}

func clamp16(v float32) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}

// upsampler is sinc for voice at a rate the codec's is a whole multiple of other than the pipeline's:
// the same polyphase low pass at the input's Nyquist, the same taps a phase and the same headroom, at
// phases of its own. Voice Quality's 24 kHz is two phases; 48 kHz is one, which is no filter at all,
// only the headroom, so every quality plays speech at the same level.
type upsampler struct {
	phases  int
	filter  []float32
	hist    [voiceTaps]float32
	clipped uint64
}

func newUpsampler(phases int) *upsampler {
	u := &upsampler{phases: phases}
	if phases == 1 {
		return u
	}
	n := phases * voiceTaps
	u.filter = make([]float32, n)
	center := float64(n-1) / 2
	for i := range u.filter {
		x := (float64(i) - center) / float64(phases)
		s := 1.0
		if x != 0 {
			s = math.Sin(math.Pi*x) / (math.Pi * x)
		}
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		u.filter[i] = float32(s * w * 0.9)
	}
	return u
}

func (u *upsampler) Reset() { u.hist = [voiceTaps]float32{} }

// Run appends the interleaved stereo result of mono to out.
func (u *upsampler) Run(mono []int16, out []int16) []int16 {
	for _, s := range mono {
		if u.phases == 1 {
			v := clamp16(float32(s) * 0.9)
			out = append(out, v, v)
			continue
		}
		copy(u.hist[:], u.hist[1:])
		u.hist[voiceTaps-1] = float32(s)
		for p := range u.phases {
			var acc float32
			for k := range voiceTaps {
				acc += u.filter[p+k*u.phases] * u.hist[voiceTaps-1-k]
			}
			if acc > math.MaxInt16 || acc < math.MinInt16 {
				u.clipped++
			}
			v := clamp16(acc)
			out = append(out, v, v)
		}
	}
	return out
}
