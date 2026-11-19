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
		ico := ICO(Image(st, 2, white))
		// header(6) + entry(16) + BITMAPINFOHEADER(40) + 32*32*4 + mask 4*32
		if binary.LittleEndian.Uint16(ico[2:]) != 1 || len(ico) != 6+16+40+4096+128 ||
			binary.LittleEndian.Uint32(ico[22:]) != 40 || int32(binary.LittleEndian.Uint32(ico[30:])) != 64 {
			t.Fatalf("%s: bad ico (%d bytes)", st, len(ico))
		}
	}
	// Screen colour follows the state.
	img := Image(session.Waiting, 1, white)
	if c := img.RGBAAt(3, 3); c.R != 0xfb {
		t.Fatalf("waiting screen %v", c)
	}
}
