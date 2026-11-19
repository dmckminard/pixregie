package main

import (
	"bytes"
	"context"
	"flag"
	"image"
	"image/png"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/fb"
	"github.com/dmckminard/pixregie/internal/screen"
	"github.com/dmckminard/pixregie/internal/session"
)

// frameEvery paces the two-frame animations (screens, bubbles, blinking).
const frameEvery = 600 * time.Millisecond

// painter renders the room and pushes it to the framebuffer. The latest
// frame is also kept for the debug page's live preview.
type painter struct {
	store  *collector.Store
	pairer *collector.Pairer
	fbDev  string
	log    *log.Logger

	mu         sync.Mutex
	last       *image.RGBA
	sleepAfter time.Duration // 0: never sleep
	hideTitles bool
}

func (p *painter) setHideTitles(v bool) {
	p.mu.Lock()
	p.hideTitles = v
	p.mu.Unlock()
}

func (p *painter) setSleepAfter(d time.Duration) {
	p.mu.Lock()
	p.sleepAfter = d
	p.mu.Unlock()
}

// active reports whether the board has something to say: a session that
// works, waits or is stuck, or a pairing in progress.
func active(st collector.State, pairing bool) bool {
	if pairing {
		return true
	}
	for _, m := range st.Machines {
		if !m.Online {
			continue
		}
		for _, s := range m.Sessions {
			if s.Status != session.Idle {
				return true
			}
		}
	}
	return false
}

// shiftEvery moves the picture by a couple of pixels now and then, so
// static parts (header, walls) do not mark the panel.
const shiftEvery = 10 * time.Minute

var shifts = [][2]int{{0, 0}, {2, 0}, {2, 2}, {0, 2}}

func (p *painter) run(ctx context.Context) {
	var dev *fb.Device
	if p.fbDev != "" {
		d, err := fb.Open(p.fbDev)
		if err != nil {
			p.log.Printf("no screen output (%v): debug page preview only", err)
		} else {
			dev = d
			defer dev.Close()
			hideConsoleCursor()
			p.log.Printf("drawing on %s (%dx%d, %d bpp)", p.fbDev, dev.W, dev.H, dev.BPP)
		}
	}
	scale := 1.0
	if dev != nil {
		var k int
		scale, k = screen.BestScale(dev.W, dev.H)
		p.log.Printf("rendering at %dx%d, upscaled x%d", int(screen.Width*scale), int(screen.Height*scale), k)
	}
	host, _ := os.Hostname()
	t := time.NewTicker(frameEvery)
	defer t.Stop()
	lastActive := time.Now()
	asleep := false
	for frame := 0; ; frame++ {
		now := time.Now()
		p.mu.Lock()
		hide := p.hideTitles
		p.mu.Unlock()
		opt := screen.Options{Frame: frame, Night: isNight(now), Now: now, Host: host + ".local", Scale: scale, HideTitles: hide}
		req, pairing := p.pairer.Pending()
		if pairing {
			opt.Pair = &req
		}
		st := p.store.Snapshot()
		img := screen.Render(st, opt)
		p.mu.Lock()
		p.last = img
		sleepAfter := p.sleepAfter
		p.mu.Unlock()

		if active(st, pairing) {
			lastActive = now
		}
		sleep := sleepAfter > 0 && now.Sub(lastActive) > sleepAfter
		if dev != nil {
			if sleep != asleep {
				if err := dev.Blank(sleep); err != nil {
					p.log.Printf("screen %s: %v", map[bool]string{true: "sleep", false: "wake"}[sleep], err)
				}
				p.log.Printf("screen %s", map[bool]string{true: "asleep", false: "awake"}[sleep])
			}
			if !sleep {
				sh := shifts[int(now.Unix()/int64(shiftEvery.Seconds()))%len(shifts)]
				if err := dev.Show(img, sh[0], sh[1]); err != nil {
					p.log.Printf("framebuffer: %v", err)
				}
			}
		}
		asleep = sleep
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *painter) servePNG(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	img := p.last
	p.mu.Unlock()
	if img == nil {
		http.Error(w, "no frame yet", http.StatusServiceUnavailable)
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

// Night mode follows the real clock: lamps on from 19:00 to 07:00.
func isNight(t time.Time) bool { h := t.Hour(); return h >= 19 || h < 7 }

// hideConsoleCursor stops the text console's cursor blinking over the
// picture. Best effort: needs write access to tty1.
func hideConsoleCursor() {
	if f, err := os.OpenFile("/dev/tty1", os.O_WRONLY, 0); err == nil {
		f.WriteString("\x1b[?25l")
		f.Close()
	}
}

func renderPNG(args []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	out := fs.String("o", "pixregie.png", "output PNG")
	night := fs.Bool("night", true, "night mode")
	frame := fs.Int("frame", 0, "animation frame")
	scale := fs.Float64("scale", 1, "1 (1280x720) or 1.5 (1920x1080)")
	fs.Parse(args)
	now := time.Now()
	img := screen.Render(screen.Demo(now), screen.Options{Frame: *frame, Night: *night, Now: now, Host: "pixregie.local", Scale: *scale})
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
