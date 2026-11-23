package screen

import (
	"fmt"
	"testing"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/session"
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

func TestVisible(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	mk := func(id string, st session.Status, age time.Duration) session.Session {
		return session.Session{ID: id, Status: st, Modified: now.Add(-age)}
	}
	got := visible([]session.Session{
		mk("old-idle", session.Idle, 3*time.Hour),
		mk("idle", session.Idle, 10*time.Minute),
		mk("work", session.Working, time.Second),
		mk("wait", session.Waiting, time.Minute),
		mk("stale", session.Stale, 20*time.Minute),
	}, Options{Now: now, Policy: Policy{IdleHide: time.Hour}})
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if fmt.Sprint(ids) != "[wait stale work idle]" {
		t.Fatalf("got %v", ids)
	}
}

func TestPolicy(t *testing.T) {
	now := time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC)
	p := Policy{IdleHide: 4 * time.Hour, StaleHide: time.Hour, Pinned: map[string]bool{"pin": true}}
	cases := []struct {
		s    session.Session
		show bool
	}{
		{session.Session{ID: "a", Status: session.Idle, Open: true, Modified: now.Add(-79 * time.Hour)}, false},
		{session.Session{ID: "b", Status: session.Idle, Modified: now.Add(-3 * time.Hour)}, true},
		{session.Session{ID: "c", Status: session.Stale, Modified: now.Add(-2 * time.Hour)}, false},
		{session.Session{ID: "d", Status: session.Waiting, Modified: now.Add(-30 * time.Hour)}, false},
		{session.Session{ID: "e", Status: session.Working, Modified: now.Add(-time.Minute)}, true},
		{session.Session{ID: "pin", Status: session.Idle, Modified: now.Add(-100 * time.Hour)}, true},
	}
	for _, c := range cases {
		if got := p.Shows(c.s, now); got != c.show {
			t.Errorf("%s: shows=%v", c.s.ID, got)
		}
	}
}
