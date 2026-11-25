package aec

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// Suppressor takes down the echo a Canceller leaves behind. The filter subtracts what is a linear copy
// of the reference; what a small speaker adds at volume (distortion, a room's tail past the filter's
// reach) is not, and over loud music it stays a few decibels under a voice at the microphone. A wake
// word has to be heard through that.
//
// Each bin of a short-time spectrum is turned down by how much of it is likely echo: the echo the
// filter predicted, times how much of that prediction usually leaks through. The leak is learned as
// the smallest ratio of what is left to what was predicted over the last two seconds, because when
// nobody is talking, what is left is all leak, and talking only ever raises the ratio. A bin is never
// turned down further than the floor, so a voice over the music loses at most that, and only in bins
// the music holds.
//
// It runs on 40 ms windows every 10 ms and gives its output 40 ms late. Not safe for concurrent use.
type Suppressor struct {
	over  float32 // how far over the estimated echo to suppress
	floor float32 // the least gain, as an amplitude

	fft     *fft.FFT
	win     []float32
	in, out []float32 // the last supWindow samples given, oldest first
	ola     []float32
	x, e    []complex64

	ratio, gain, leak []float32
	cur               []float32   // smallest smoothed ratio in the current part of the minimum's span
	parts             [][]float32 // the smallest of each finished part, oldest first
	inPart            int

	pendIn, pendOut []float32 // samples short of a whole hop
	ready           []int16   // output not yet handed back
	res             []int16   // what Process hands back, reused
}

const (
	supWindow = 640 // 40 ms at 16 kHz
	supHop    = 160 // 10 ms
	supFFT    = 1024
	supBins   = supFFT/2 + 1

	// The leak is the minimum over supParts parts of supPartHops hops each: two seconds, so it spans a
	// spoken phrase and finds the gaps around it, kept in parts so a hop costs one comparison a bin.
	supParts    = 8
	supPartHops = 25

	// supSmooth smooths the ratio of what is left to what was predicted, so one hop's chance
	// coincidence is not taken for the leak. supRelease is how fast a bin's gain comes back up; it
	// goes down at once, so the start of a burst of music is caught.
	supSmooth  = 0.2
	supRelease = 0.3
)

// SuppressorDelay is how many samples late Process gives its output: a window, less the hop that
// comes out of it, plus a hop held back so that a block that is not a whole number of hops still
// gets as many samples back as it gave.
const SuppressorDelay = supWindow

// NewSuppressor makes a suppressor. over scales the echo estimate (1 takes off what is estimated, 2
// twice that), and floorDB is the most any bin is taken down, in dB below unity (a negative number).
func NewSuppressor(over, floorDB float64) *Suppressor {
	s := &Suppressor{
		over:  float32(over),
		floor: float32(math.Pow(10, floorDB/20)),
		fft:   fft.New(supFFT),
		win:   make([]float32, supWindow),
		in:    make([]float32, supWindow),
		out:   make([]float32, supWindow),
		ola:   make([]float32, supWindow),
		x:     make([]complex64, supFFT),
		e:     make([]complex64, supFFT),
		ratio: make([]float32, supBins),
		gain:  make([]float32, supBins),
		leak:  make([]float32, supBins),
		cur:   make([]float32, supBins),
	}
	for i := range s.win {
		// The square root of a periodic Hann window, on the way in and on the way out: their product
		// is the Hann window, whose copies a quarter window apart add up to a constant.
		s.win[i] = float32(math.Sqrt(0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/supWindow)))
	}
	s.Reset()
	return s
}

// Reset forgets everything: what it learned of the leak, and the audio in flight.
func (s *Suppressor) Reset() {
	for k := range s.ratio {
		s.ratio[k], s.leak[k] = 1, 1
		s.cur[k] = float32(math.Inf(1))
	}
	s.parts = s.parts[:0]
	s.inPart = 0
	s.Restart()
}

// Restart drops the audio in flight, for a stream that stopped and will start again, and keeps what
// was learned of the leak, as the canceller keeps its filter: the room has not changed between two
// replies. The output starts SuppressorDelay samples of silence late again.
func (s *Suppressor) Restart() {
	clear(s.in)
	clear(s.out)
	clear(s.ola)
	for k := range s.gain {
		s.gain[k] = 1
	}
	s.pendIn, s.pendOut = s.pendIn[:0], s.pendOut[:0]
	s.ready = append(s.ready[:0], make([]int16, supHop)...)
}

// InFlight is what Process will hand back next, up to a hop of it at least: what a caller that stops
// using the suppressor can fade out of, rather than cutting it off where it is. The slice is the
// suppressor's own and good until the next call.
func (s *Suppressor) InFlight() []int16 { return s.ready }

// Process takes what the canceller was given (in) and what it gave back (out), the same length, and
// returns as many samples of out with the leftover echo taken down, SuppressorDelay samples late. The
// slice returned is the suppressor's own and good until the next call.
func (s *Suppressor) Process(in, out []int16) []int16 {
	n := min(len(in), len(out))
	for i := range n {
		s.pendIn = append(s.pendIn, float32(in[i]))
		s.pendOut = append(s.pendOut, float32(out[i]))
	}
	for len(s.pendIn) >= supHop {
		s.hop(s.pendIn[:supHop], s.pendOut[:supHop])
		s.pendIn = s.pendIn[:copy(s.pendIn, s.pendIn[supHop:])]
		s.pendOut = s.pendOut[:copy(s.pendOut, s.pendOut[supHop:])]
	}
	got := min(n, len(s.ready))
	s.res = append(s.res[:0], s.ready[:got]...)
	s.ready = s.ready[:copy(s.ready, s.ready[got:])]
	return s.res
}

func (s *Suppressor) hop(in, out []float32) {
	copy(s.in, s.in[supHop:])
	copy(s.in[supWindow-supHop:], in)
	copy(s.out, s.out[supHop:])
	copy(s.out[supWindow-supHop:], out)

	clear(s.x)
	clear(s.e)
	for i, w := range s.win {
		s.x[i] = complex(s.in[i]*w, 0)
		s.e[i] = complex(s.out[i]*w, 0)
	}
	s.fft.Forward(s.x)
	s.fft.Forward(s.e)

	for k := range supBins {
		e := s.e[k]
		y := s.x[k] - e // the echo the filter predicted, and took out
		pe := real(e)*real(e) + imag(e)*imag(e)
		py := real(y)*real(y) + imag(y)*imag(y)

		s.ratio[k] += (pe/(py+1) - s.ratio[k]) * supSmooth
		s.cur[k] = min(s.cur[k], s.ratio[k])
		leak := s.cur[k]
		for _, p := range s.parts {
			leak = min(leak, p[k])
		}
		s.leak[k] = min(leak, 1)

		g := max(1-s.over*s.leak[k]*py/(pe+1), s.floor)
		if g < s.gain[k] {
			s.gain[k] = g
		} else {
			s.gain[k] += (g - s.gain[k]) * supRelease
		}
		s.e[k] *= complex(s.gain[k], 0)
	}
	if s.inPart++; s.inPart == supPartHops {
		s.inPart = 0
		if len(s.parts) == supParts-1 {
			p := s.parts[0]
			s.parts = append(s.parts[:0], s.parts[1:]...)
			s.parts = append(s.parts, append(p[:0], s.cur...))
		} else {
			s.parts = append(s.parts, append([]float32(nil), s.cur...))
		}
		for k := range s.cur {
			s.cur[k] = float32(math.Inf(1))
		}
	}

	for k := 1; k < supFFT/2; k++ {
		s.e[supFFT-k] = complex(real(s.e[k]), -imag(s.e[k]))
	}
	s.fft.Inverse(s.e)
	for i, w := range s.win {
		s.ola[i] += real(s.e[i]) * w
	}
	const scale = 2 * float32(supHop) / supWindow // the Hann copies sum to supWindow/(2·supHop)
	for i := range supHop {
		v := s.ola[i] * scale
		s.ready = append(s.ready, int16(max(-32768, min(32767, v))))
	}
	copy(s.ola, s.ola[supHop:])
	clear(s.ola[supWindow-supHop:])
}
