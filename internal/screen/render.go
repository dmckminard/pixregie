// Package screen draws the pixel-art control room ("la régie") from the
// collector state. It is pure drawing: no I/O, deterministic for a given
// state, frame and time, so it is easy to test and to preview as a PNG.
package screen

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/session"
)

// The scene is authored at 1280×720, the HDMI mode the Pi is set to.
const (
	Width  = 1280
	Height = 720
)

// Options vary the picture without changing the state.
type Options struct {
	Frame int       // animation frame counter (~0.6 s per step)
	Night bool      // lamps on, darker room
	Now   time.Time // clock in the header, ages
	Host  string    // shown in the footer, e.g. "pixregie.local"
}

func hex(s string) color.RGBA {
	var r, g, b uint8
	fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b)
	return color.RGBA{r, g, b, 255}
}

var (
	cOutline = hex("#0c0a12")
	cText    = hex("#e8e4f4")
	cMuted   = hex("#8a84a3")
	cBarLine = hex("#2f2b40")

	machineAccents = []color.RGBA{hex("#6ea8ff"), hex("#b48cff"), hex("#5fd4c4"), hex("#f9a8d4"), hex("#fdba74")}
)

type statusStyle struct {
	screen, line, label color.RGBA
	word                string
}

var styles = map[session.Status]statusStyle{
	session.Working: {hex("#4ade80"), hex("#14532d"), hex("#4ade80"), "en cours"},
	session.Waiting: {hex("#fbbf24"), hex("#422006"), hex("#fbbf24"), "attend"},
	session.Stale:   {hex("#f87171"), hex("#450a0a"), hex("#f87171"), "figée"},
	session.Idle:    {hex("#2a2638"), hex("#6b6585"), cMuted, "inactive"},
}

// Header order and the colour of the counter squares.
var counterOrder = []struct {
	st  session.Status
	dot color.RGBA
}{
	{session.Working, hex("#4ade80")},
	{session.Waiting, hex("#fbbf24")},
	{session.Stale, hex("#f87171")},
	{session.Idle, hex("#4b4763")},
}

type theme struct{ bg, wall, floor, floorEdge, deskTop, desk color.RGBA }

var (
	nightTheme = theme{hex("#14121c"), hex("#1f1c2b"), hex("#191624"), hex("#24203a"), hex("#4e4670"), hex("#3b3552")}
	dayTheme   = theme{hex("#2b2840"), hex("#3a3654"), hex("#322e4a"), hex("#3d3858"), hex("#6a5f96"), hex("#524876")}
)

// Render returns the full 1280×720 frame.
func Render(st collector.State, o Options) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))
	th := dayTheme
	if o.Night {
		th = nightTheme
	}
	fill(img, img.Bounds(), th.bg)

	counts := map[session.Status]int{}
	nSessions := 0
	for _, m := range st.Machines {
		if !m.Online {
			continue
		}
		for _, s := range m.Sessions {
			counts[s.Status]++
			nSessions++
		}
	}

	drawHeader(img, counts, o)
	drawFooter(img, len(st.Machines), nSessions, o)

	area := image.Rect(16, 60+16, Width-16, Height-32-16)
	if len(st.Machines) == 0 {
		msg := "En attente des agents…"
		vt20.draw(img, area.Min.X+(area.Dx()-vt20.width(msg, 0))/2, area.Min.Y+area.Dy()/2-10, msg, cMuted, 0)
		return img
	}
	n := len(st.Machines)
	if n > 4 {
		n = 4 // more would not fit legibly at 1280 px
	}
	gap := 16
	w := (area.Dx() - gap*(n-1)) / n
	for i := 0; i < n; i++ {
		x := area.Min.X + i*(w+gap)
		drawRoom(img, image.Rect(x, area.Min.Y, x+w, area.Max.Y), st.Machines[i], machineAccents[i%len(machineAccents)], th, o)
	}
	return img
}

func drawHeader(img *image.RGBA, counts map[session.Status]int, o Options) {
	fill(img, image.Rect(0, 0, Width, 56), cOutline)
	fill(img, image.Rect(0, 56, Width, 60), cBarLine)

	x := 24
	x += silk24b.draw(img, x, (56-silk24b.height)/2+2, "PIXREGIE", cText, 4)
	vt20.draw(img, x+16, (56-vt20.height)/2+2, "la régie de tes sessions Claude Code", cMuted, 0)

	// Right side, laid out from the right edge.
	clock := o.Now.Format("15:04")
	right := Width - 24
	cw := silk16.width(clock, 0)
	silk16.draw(img, right-cw, (56-silk16.height)/2+2, clock, cMuted, 0)
	right -= cw + 16
	fill(img, image.Rect(right-4, 14, right, 42), cBarLine)
	right -= 4 + 24
	for i := len(counterOrder) - 1; i >= 0; i-- {
		c := counterOrder[i]
		label := fmt.Sprintf("%d %s", counts[c.st], styles[c.st].word)
		lw := vt20.width(label, 0)
		vt20.draw(img, right-lw, (56-vt20.height)/2+2, label, cText, 0)
		right -= lw + 8
		fill(img, image.Rect(right-12, 22, right, 34), c.dot)
		right -= 12 + 24
	}
}

func drawFooter(img *image.RGBA, machines, sessions int, o Options) {
	y := Height - 32
	fill(img, image.Rect(0, y, Width, Height), cOutline)
	host := o.Host
	if host == "" {
		host = "pixregie"
	}
	left := fmt.Sprintf("%s · %d machine%s · %d session%s", host, machines, plural(machines), sessions, plural(sessions))
	vt20.draw(img, 24, y+6, left, cMuted, 0)
	right := "mis à jour " + o.Now.Format("15:04:05")
	vt20.draw(img, Width-24-vt20.width(right, 0), y+6, right, cMuted, 0)
}

func drawRoom(img *image.RGBA, r image.Rectangle, m collector.Machine, accent color.RGBA, th theme, o Options) {
	// Frame: 4 px outline, 4 px inner edge, then the floor.
	fill(img, r, cOutline)
	fill(img, r.Inset(4), th.floorEdge)
	in := r.Inset(8)
	fill(img, in, th.floor)

	// Back wall with name plate and the ON AIR light.
	wall := image.Rect(r.Min.X+4, r.Min.Y+4, r.Max.X-4, r.Min.Y+4+72)
	fill(img, wall, th.wall)
	fill(img, image.Rect(wall.Min.X, wall.Max.Y, wall.Max.X, wall.Max.Y+4), cOutline)

	fill(img, image.Rect(wall.Min.X+12, wall.Min.Y+14, wall.Min.X+24, wall.Min.Y+26), accent)
	silk16.draw(img, wall.Min.X+32, wall.Min.Y+9, silk16.fit(strings.ToUpper(m.Name), wall.Dx()-150), cText, 0)
	sub := "en ligne"
	if !m.Online {
		sub = "hors ligne · " + ago(o.Now.Sub(m.LastSeen))
	}
	vt20.draw(img, wall.Min.X+12, wall.Min.Y+38, sub, cMuted, 0)

	onAir := false
	for _, s := range m.Sessions {
		if s.Status == session.Working {
			onAir = m.Online
		}
	}
	airBg, airFg := hex("#2a1820"), hex("#6b4a55")
	if onAir {
		airBg, airFg = hex("#dc2626"), hex("#fff1f1")
	}
	aw := silk16.width("ON AIR", 2) + 16 + 8
	badge := image.Rect(wall.Max.X-12-aw, wall.Min.Y+14, wall.Max.X-12, wall.Min.Y+14+silk16.height+8+4)
	fill(img, badge, cOutline)
	fill(img, badge.Inset(4), airBg)
	silk16.draw(img, badge.Min.X+12, badge.Min.Y+6, "ON AIR", airFg, 2)

	// Desks: two columns of 192 px rows.
	desks := image.Rect(in.Min.X+4, wall.Max.Y+4+12, in.Max.X-4, in.Max.Y-12)
	colW := (desks.Dx() - 8) / 2
	rows := (desks.Dy() + 8) / 200
	if rows < 1 {
		rows = 1
	}
	capacity := rows * 2
	list := m.Sessions
	extra := 0
	if len(list) > capacity {
		extra = len(list) - (capacity - 1)
		list = list[:capacity-1]
	}
	for i, s := range list {
		x := desks.Min.X + (i%2)*(colW+8)
		y := desks.Min.Y + (i/2)*200
		drawDesk(img, x, y, colW, s, i, accent, th, o)
	}
	if extra > 0 {
		i := len(list)
		x := desks.Min.X + (i%2)*(colW+8)
		y := desks.Min.Y + (i/2)*200
		t := fmt.Sprintf("+%d", extra)
		silk24b.draw(img, x+(colW-silk24b.width(t, 0))/2, y+70, t, cMuted, 0)
	}

	if !m.Online {
		dim(img, r, th.bg, 0.55)
	}
}

func drawDesk(img *image.RGBA, x, y, w int, s session.Session, i int, accent color.RGBA, th theme, o Options) {
	st, ok := styles[s.Status]
	if !ok {
		st = styles[session.Idle]
	}
	if o.Night && s.Status != session.Idle {
		lamp(img, x+92, y+70, 70, hex("#fbd678"), 0.16)
	}
	if s.Status == session.Waiting {
		bob := 0
		if o.Frame%2 == 1 {
			bob = -4
		}
		drawSprite(img, x+50, y+bob, spriteBubble, map[byte]color.RGBA{'o': cOutline, 'W': hex("#fff7e0"), 'A': hex("#b45309")}, 4)
	}

	look := looks[i%len(looks)]
	op := spriteOperator
	if s.Status == session.Idle || s.Status == session.Stale {
		op = spriteOperatorAsleep
	}
	drawSprite(img, x+36, y+36, op, map[byte]color.RGBA{
		'h': look.hair, 's': look.skin, 'e': cOutline, 'd': cOutline, 'k': cOutline, 'c': accent,
	}, 5)

	screenCol, lineCol := st.screen, st.line
	mon := spriteMonitorIdle
	switch s.Status {
	case session.Working:
		mon = spriteMonitorA
		if o.Frame%2 == 1 {
			mon = spriteMonitorB
		}
	case session.Waiting:
		mon = spriteMonitorAlert
	case session.Stale:
		mon = spriteMonitorAlert
		if o.Frame%2 == 1 {
			screenCol = mix(screenCol, th.floor, 0.85)
			lineCol = mix(lineCol, th.floor, 0.85)
		}
	}
	drawSprite(img, x+104, y+46, mon, map[byte]color.RGBA{'o': cOutline, 'S': screenCol, 'L': lineCol}, 5)

	// Desk: top (outlined) and front.
	fill(img, image.Rect(x+12, y+86, x+12+168, y+86+20), cOutline)
	fill(img, image.Rect(x+16, y+90, x+16+160, y+90+12), th.deskTop)
	fill(img, image.Rect(x+20, y+106, x+20+152, y+106+28), cOutline)
	fill(img, image.Rect(x+24, y+106, x+24+144, y+106+24), th.desk)

	maxW := w - 16
	if maxW > 176 {
		maxW = 176
	}
	silk16.draw(img, x+12, y+142, silk16.fit(s.Project, maxW), cText, 0)
	vt20.draw(img, x+12, y+164, vt20.fit(label(s, o.Now), maxW), st.label, 0)
}

// label is the small line under each desk.
func label(s session.Session, now time.Time) string {
	switch s.Status {
	case session.Working:
		if s.Tool != "" {
			return s.Tool
		}
		return "réfléchit…"
	case session.Waiting:
		return "attend ton OK"
	case session.Stale:
		return "figée · " + ago(now.Sub(s.Modified))
	default:
		return "à toi de jouer"
	}
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
}

func plural(n int) string {
	if n > 1 {
		return "s"
	}
	return ""
}
