package icon

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"

	"github.com/dmckminard/pixregie/internal/session"
)

func TestIcons(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	for _, st := range []session.Status{session.Working, session.Waiting, session.Stale, session.Idle, Disconnected} {
		p := PNG(st, 2, white)
		img, err := png.Decode(bytes.NewReader(p))
		if err != nil || img.Bounds().Dx() != 32 {
			t.Fatalf("%s: %v", st, err)
		}
		ico := ICO(p, 32)
		if binary.LittleEndian.Uint16(ico[2:]) != 1 || !bytes.Equal(ico[22:], p) {
			t.Fatalf("%s: bad ico", st)
		}
	}
	// Screen colour follows the state.
	img := Image(session.Waiting, 1, white)
	if c := img.RGBAAt(3, 3); c.R != 0xfb {
		t.Fatalf("waiting screen %v", c)
	}
}
