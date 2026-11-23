package mic

import (
	"encoding/binary"
	"testing"
)

// The header says what the capture blocks are: their channels, rate and sample size, and how much
// of them follows.
func TestRawCaptureHeader(t *testing.T) {
	h := wavHeader(4, 16000, 24, 1200)
	if string(h[0:4]) != "RIFF" || string(h[8:16]) != "WAVEfmt " || string(h[36:40]) != "data" {
		t.Fatalf("not a WAVE header: %q", h)
	}
	for _, c := range []struct {
		at   int
		got  uint32
		want uint32
	}{
		{4, binary.LittleEndian.Uint32(h[4:]), 36 + 1200},
		{22, uint32(binary.LittleEndian.Uint16(h[22:])), 4},
		{24, binary.LittleEndian.Uint32(h[24:]), 16000},
		{28, binary.LittleEndian.Uint32(h[28:]), 16000 * 4 * 3},
		{32, uint32(binary.LittleEndian.Uint16(h[32:])), 12},
		{34, uint32(binary.LittleEndian.Uint16(h[34:])), 24},
		{40, binary.LittleEndian.Uint32(h[40:]), 1200},
	} {
		if c.got != c.want {
			t.Errorf("byte %d: %d, want %d", c.at, c.got, c.want)
		}
	}
}
