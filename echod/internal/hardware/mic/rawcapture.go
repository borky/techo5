package mic

// Raw capture, a fork-only diagnostic (FORK.md): the capture blocks exactly as they arrive, every
// microphone and the playback loopback, written to a WAVE file, so the echo canceller and the wake
// word can be tried offline on one recording instead of a person saying the wake word again for each
// change.
//
// It runs only when asked, by a request file in the state directory holding how many seconds to keep
// (at most rawMaxSeconds); the file is removed as the recording starts. What is written is taken after
// the software mute, so a muted device records silence like everything else downstream. The recording
// stays on the device: nothing here sends it anywhere.

import (
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

const (
	rawRequest    = "raw-capture.request"
	rawOut        = "raw-capture.wav"
	rawMaxSeconds = 180
)

// rawCapture is the recording in progress, if any. It is used from the capture goroutine only, under
// the source's lock.
type rawCapture struct {
	frames int // capture frames seen, to look for a request about once a second
	f      *os.File
	left   int // bytes still to write
	data   int // bytes written
}

// add looks for a request now and then, and writes the block while a recording runs.
func (r *rawCapture) add(raw []byte) {
	frameBytes := Channels * Bits / 8
	if r.f == nil {
		r.frames += len(raw) / frameBytes
		if r.frames < Rate {
			return
		}
		r.frames = 0
		r.start(frameBytes)
		return
	}
	n := min(len(raw), r.left)
	if _, err := r.f.Write(raw[:n]); err != nil {
		slog.Error("raw capture: writing failed", "err", err)
		r.finish()
		return
	}
	r.left -= n
	r.data += n
	if r.left == 0 {
		r.finish()
	}
}

func (r *rawCapture) start(frameBytes int) {
	req := filepath.Join(layout.StateDir, rawRequest)
	b, err := os.ReadFile(req)
	if err != nil {
		return
	}
	_ = os.Remove(req)
	secs, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || secs <= 0 {
		secs = 60
	}
	secs = min(secs, rawMaxSeconds)
	f, err := os.Create(filepath.Join(layout.StateDir, rawOut))
	if err != nil {
		slog.Error("raw capture: cannot create the file", "err", err)
		return
	}
	if _, err := f.Write(wavHeader(Channels, Rate, Bits, 0)); err != nil {
		f.Close()
		return
	}
	r.f, r.left, r.data = f, secs*Rate*frameBytes, 0
	slog.Info("raw capture started", "seconds", secs, "channels", Channels, "mics", Mics, "ref_first", RefFirst)
}

func (r *rawCapture) finish() {
	if _, err := r.f.WriteAt(wavHeader(Channels, Rate, Bits, r.data), 0); err != nil {
		slog.Error("raw capture: finishing the header failed", "err", err)
	}
	r.f.Close()
	slog.Info("raw capture written", "file", filepath.Join(layout.StateDir, rawOut), "bytes", r.data)
	r.f = nil
}

// wavHeader is a 44-byte PCM WAVE header for data bytes of interleaved samples.
func wavHeader(channels, rate, bits, data int) []byte {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+data))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*channels*bits/8))
	binary.LittleEndian.PutUint16(h[32:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(h[34:], uint16(bits))
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(data))
	return h
}
