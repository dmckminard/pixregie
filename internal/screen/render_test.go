package screen

import (
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

func TestRenderDoesNotPanic(t *testing.T) {
	now := time.Date(2026, 11, 19, 23, 40, 0, 0, time.UTC)
	for _, st := range []collector.State{{}, Demo(now)} {
		for f := 0; f < 2; f++ {
			img := Render(st, Options{Frame: f, Night: f == 0, Now: now})
			if img.Bounds().Dx() != Width || img.Bounds().Dy() != Height {
				t.Fatal("bad size")
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
