package screen

import (
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

func TestRenderDoesNotPanic(t *testing.T) {
	now := time.Date(2026, 11, 19, 23, 40, 0, 0, time.UTC)
	for _, scale := range Scales {
		for _, st := range []collector.State{{}, Demo(now)} {
			for f := 0; f < 2; f++ {
				img := Render(st, Options{Frame: f, Night: f == 0, Now: now, Scale: scale})
				if img.Bounds().Dx() != int(Width*scale) || img.Bounds().Dy() != int(Height*scale) {
					t.Fatalf("bad size at x%v: %v", scale, img.Bounds())
				}
			}
		}
	}
}

func TestFitEllipsis(t *testing.T) {
	s := silk16.fit("un-nom-de-projet-vraiment-tres-long", 120)
	if silk16.width(s, 0) > 120 || s[len(s)-3:] != "…" {
		t.Fatalf("%q", s)
	}
}

func TestBestScale(t *testing.T) {
	cases := []struct {
		w, h   int
		scale  float64
		factor int
	}{
		{1280, 720, 1, 1},
		{1920, 1080, 1.5, 1},
		{1920, 1200, 1.5, 1},
		{2560, 1440, 1, 2},
		{3840, 2160, 1.5, 2},
		{1024, 768, 1, 1}, // smaller than the board: falls back to x1
	}
	for _, c := range cases {
		s, k := BestScale(c.w, c.h)
		if s != c.scale || k != c.factor {
			t.Errorf("%dx%d: got x%v*%d, want x%v*%d", c.w, c.h, s, k, c.scale, c.factor)
		}
	}
}
