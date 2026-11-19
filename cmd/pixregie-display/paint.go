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

	mu   sync.Mutex
	last *image.RGBA
}

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
	for frame := 0; ; frame++ {
		now := time.Now()
		opt := screen.Options{Frame: frame, Night: isNight(now), Now: now, Host: host + ".local", Scale: scale}
		if req, ok := p.pairer.Pending(); ok {
			opt.Pair = &req
		}
		img := screen.Render(p.store.Snapshot(), opt)
		p.mu.Lock()
		p.last = img
		p.mu.Unlock()
		if dev != nil {
			if err := dev.Show(img); err != nil {
				p.log.Printf("framebuffer: %v", err)
			}
		}
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
