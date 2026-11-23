// Package screen draws the pixel-art control room ("la régie") from the
// collector state. It is pure drawing: no I/O, deterministic for a given
// state, frame and time, so it is easy to test and to preview as a PNG.
package screen

import (
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/session"
)

// Options vary the picture without changing the state.
type Options struct {
	Frame int       // animation frame counter (~0.6 s per step)
	Night bool      // lamps on, darker room
	Now   time.Time // clock in the header, ages
	Host  string    // shown in the footer, e.g. "pixregie.local"
	Scale float64   // 1 → 1280×720, 1.5 → 1920×1080
	// HideTitles leaves session titles off the screen.
	HideTitles bool
	// Pair, when set, shows the pairing code over the room.
	Pair *collector.PairRequest
	// Policy decides which sessions are worth showing.
	Policy Policy
}

// Policy says which sessions the board shows. Machines send every session
// of the last week; the Pi decides, so delays and pins are set in one
// place (its page).
type Policy struct {
	IdleHide  time.Duration   // finished sessions, after this long (default 1 h)
	StaleHide time.Duration   // stuck sessions: the window was likely closed mid-turn (default 1 h)
	Pinned    map[string]bool // session ids always shown
}

// WaitHide: a question or permission unanswered for a day belongs to a
// session that was closed.
const WaitHide = 24 * time.Hour

// Shows reports whether s is on the board.
func (p Policy) Shows(s session.Session, now time.Time) bool {
	if p.Pinned[s.ID] {
		return true
	}
	age := now.Sub(s.Modified)
	switch s.Status {
	case session.Idle:
		return age <= orDefault(p.IdleHide, time.Hour)
	case session.Stale:
		return age <= orDefault(p.StaleHide, time.Hour)
	case session.Waiting:
		return age <= WaitHide
	}
	return true
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Visible keeps the sessions of a state the policy shows.
func Visible(st collector.State, p Policy, now time.Time) collector.State {
	out := st
	out.Machines = make([]collector.Machine, len(st.Machines))
	for i, m := range st.Machines {
		var keep []session.Session
		for _, s := range m.Sessions {
			if p.Shows(s, now) {
				keep = append(keep, s)
			}
		}
		m.Sessions = keep
		out.Machines[i] = m
	}
	return out
}

// visible filters and orders one machine's sessions for the board: most
// urgent first (waiting, stale, working, idle), then most recent.
func visible(list []session.Session, o Options) []session.Session {
	rank := map[session.Status]int{session.Waiting: 0, session.Stale: 1, session.Working: 2, session.Idle: 3}
	out := make([]session.Session, 0, len(list))
	for _, s := range list {
		if !o.Policy.Shows(s, o.Now) {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		return a.Modified.After(b.Modified)
	})
	return out
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

var counterOrder = []struct {
	st  session.Status
	dot color.RGBA
}{
	{session.Working, hex("#4ade80")},
	{session.Waiting, hex("#fbbf24")},
	{session.Stale, hex("#f87171")},
	{session.Idle, hex("#4b4763")},
}

// UrgentAfter is how long a session may wait for an answer before its
// desk starts flashing.
const UrgentAfter = 5 * time.Minute

type theme struct{ bg, wall, floor, floorEdge, deskTop, desk color.RGBA }

var (
	nightTheme = theme{hex("#14121c"), hex("#1f1c2b"), hex("#191624"), hex("#24203a"), hex("#4e4670"), hex("#3b3552")}
	dayTheme   = theme{hex("#2b2840"), hex("#3a3654"), hex("#322e4a"), hex("#3d3858"), hex("#6a5f96"), hex("#524876")}
)

// Render returns one frame, 1280×720 × Options.Scale.
func Render(st collector.State, o Options) *image.RGBA {
	if o.Scale == 0 {
		o.Scale = 1
	}
	machines := make([]collector.Machine, len(st.Machines))
	for i, m := range st.Machines {
		m.Sessions = visible(m.Sessions, o)
		machines[i] = m
	}
	st.Machines = machines
	c := newCanvas(o.Scale)
	th := dayTheme
	if o.Night {
		th = nightTheme
	}
	c.fill(0, 0, Width, Height, th.bg)

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

	// Only machines with something to show get a room. The others are a
	// line in the footer: servers that answer ("au repos") or not ("hors
	// ligne": they run all day, so that is a fault). A Mac or PC that is
	// off is normal and not shown at all. Colours stay tied to the
	// machine, not the slot.
	var rooms, resting []placed
	for i, m := range st.Machines {
		p := placed{m, machineAccents[i%len(machineAccents)]}
		switch {
		case len(m.Sessions) > 0:
			rooms = append(rooms, p)
		case m.Online || m.Server:
			resting = append(resting, p)
		}
	}

	c.header(counts, o)
	c.footer(len(st.Machines), nSessions, resting, o)

	area := image.Rect(16, 60+16, Width-16, Height-32-16)
	if len(rooms) == 0 {
		msg := "En attente des agents…"
		if len(st.Machines) > 0 {
			msg = "Aucune session en cours"
		}
		c.text(c.f.body, area.Min.X+(area.Dx()-c.textWidth(c.f.body, msg, 0))/2, area.Min.Y+area.Dy()/2-10, msg, cMuted, 0)
		if o.Pair != nil {
			c.pairing(*o.Pair, th)
		}
		return c.img
	}
	defer func() {
		if o.Pair != nil {
			c.pairing(*o.Pair, th)
		}
	}()
	n := len(rooms)
	if n > 4 {
		n = 4 // more would not fit legibly
	}
	gap := 16
	w := (area.Dx() - gap*(n-1)) / n
	for i := 0; i < n; i++ {
		x := area.Min.X + i*(w+gap)
		c.room(image.Rect(x, area.Min.Y, x+w, area.Max.Y), rooms[i].m, rooms[i].accent, th, o)
	}
	return c.img
}

type placed struct {
	m      collector.Machine
	accent color.RGBA
}

// vcenter returns the y that centres text of font f in a band [y0, y0+h).
func (c *canvas) vcenter(f *font, y0, h int) int { return y0 + (h-c.textHeight(f))/2 + 2 }

func (c *canvas) header(counts map[session.Status]int, o Options) {
	c.fill(0, 0, Width, 56, cOutline)
	c.fill(0, 56, Width, 60, cBarLine)

	c.text(c.f.title, 24, c.vcenter(c.f.title, 0, 56), "PIXREGIE", cText, 4)

	clock := o.Now.Format("15:04")
	right := Width - 24
	cw := c.textWidth(c.f.small, clock, 0)
	c.text(c.f.small, right-cw, c.vcenter(c.f.small, 0, 56), clock, cMuted, 0)
	right -= cw + 16
	c.fill(right-4, 14, right, 42, cBarLine)
	right -= 4 + 24
	for i := len(counterOrder) - 1; i >= 0; i-- {
		k := counterOrder[i]
		label := fmt.Sprintf("%d %s", counts[k.st], styles[k.st].word)
		lw := c.textWidth(c.f.body, label, 0)
		c.text(c.f.body, right-lw, c.vcenter(c.f.body, 0, 56), label, cText, 0)
		right -= lw + 8
		c.fill(right-12, 22, right, 34, k.dot)
		right -= 12 + 24
	}
}

func (c *canvas) footer(machines, sessions int, resting []placed, o Options) {
	y := Height - 32
	c.fill(0, y, Width, Height, cOutline)
	host := o.Host
	if host == "" {
		host = "pixregie"
	}
	left := fmt.Sprintf("%s · %d machine%s · %d session%s", host, machines, plural(machines), sessions, plural(sessions))
	ty := c.vcenter(c.f.body, y, 32) - 2
	x := 24 + c.text(c.f.body, 24, ty, left, cMuted, 0)
	right := "mis à jour " + o.Now.Format("15:04:05")
	rx := Width - 24 - c.textWidth(c.f.body, right, 0)
	c.text(c.f.body, rx, ty, right, cMuted, 0)

	// Machines without a room: name and whether they are reachable.
	for _, p := range resting {
		state := "au repos"
		if !p.m.Online {
			state = "hors ligne"
			if !p.m.LastSeen.IsZero() {
				state += " · " + ago(o.Now.Sub(p.m.LastSeen))
			}
		}
		name := p.m.Name + " "
		nw := c.textWidth(c.f.body, name, 0)
		need := 32 + 12 + 8 + nw + c.textWidth(c.f.body, state, 0)
		if x+need > rx-24 {
			break
		}
		x += 32
		c.fill(x, y+10, x+12, y+22, p.accent)
		x += 12 + 8
		x += c.text(c.f.body, x, ty, name, cText, 0)
		x += c.text(c.f.body, x, ty, state, cMuted, 0)
	}
}

func (c *canvas) room(r image.Rectangle, m collector.Machine, accent color.RGBA, th theme, o Options) {
	c.fill(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y, cOutline)
	c.fill(r.Min.X+4, r.Min.Y+4, r.Max.X-4, r.Max.Y-4, th.floorEdge)
	in := r.Inset(8)
	c.fill(in.Min.X, in.Min.Y, in.Max.X, in.Max.Y, th.floor)

	wall := image.Rect(r.Min.X+4, r.Min.Y+4, r.Max.X-4, r.Min.Y+4+72)
	c.fill(wall.Min.X, wall.Min.Y, wall.Max.X, wall.Max.Y, th.wall)
	c.fill(wall.Min.X, wall.Max.Y, wall.Max.X, wall.Max.Y+4, cOutline)

	c.fill(wall.Min.X+12, wall.Min.Y+14, wall.Min.X+24, wall.Min.Y+26, accent)
	c.text(c.f.small, wall.Min.X+32, wall.Min.Y+9, c.fit(c.f.small, strings.ToUpper(m.Name), wall.Dx()-150), cText, 0)
	sub := "en ligne"
	if !m.Online {
		sub = "hors ligne · " + ago(o.Now.Sub(m.LastSeen))
	}
	c.text(c.f.body, wall.Min.X+12, wall.Min.Y+38, sub, cMuted, 0)

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
	aw := c.textWidth(c.f.small, "ON AIR", 2) + 24
	bh := c.textHeight(c.f.small) + 12
	bx0, by0 := wall.Max.X-12-aw, wall.Min.Y+14
	c.fill(bx0, by0, wall.Max.X-12, by0+bh, cOutline)
	c.fill(bx0+4, by0+4, wall.Max.X-16, by0+bh-4, airBg)
	c.text(c.f.small, bx0+12, by0+6, "ON AIR", airFg, 2)

	desks := image.Rect(in.Min.X+4, wall.Max.Y+4+12, in.Max.X-4, in.Max.Y-12)
	// As many 184 px desks per row as the room is wide (at least 2).
	cols := (desks.Dx() + 8) / (184 + 8)
	if cols < 2 {
		cols = 2
	}
	colW := (desks.Dx() - 8*(cols-1)) / cols
	rows := (desks.Dy() + 8) / 200
	if rows < 1 {
		rows = 1
	}
	capacity := rows * cols
	list := m.Sessions
	extra := 0
	if len(list) > capacity {
		extra = len(list) - (capacity - 1)
		list = list[:capacity-1]
	}
	for i, s := range list {
		c.desk(desks.Min.X+(i%cols)*(colW+8), desks.Min.Y+(i/cols)*200, colW, s, i, accent, th, o)
	}
	if extra > 0 {
		i := len(list)
		x := desks.Min.X + (i%cols)*(colW+8)
		y := desks.Min.Y + (i/cols)*200
		t := fmt.Sprintf("+%d", extra)
		c.text(c.f.title, x+(colW-c.textWidth(c.f.title, t, 0))/2, y+70, t, cMuted, 0)
	}

	if !m.Online {
		c.dim(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y, th.bg, 0.55)
	}
}

func (c *canvas) desk(x, y, w int, s session.Session, i int, accent color.RGBA, th theme, o Options) {
	st, ok := styles[s.Status]
	if !ok {
		st = styles[session.Idle]
	}
	// Waiting for a while: the desk flashes so it shows from across the
	// room.
	urgent := s.Status == session.Waiting && o.Now.Sub(s.Modified) >= UrgentAfter
	flash := urgent && o.Frame%2 == 0
	if flash {
		c.lamp(x+92, y+70, 120, hex("#fbbf24"), 0.45)
	} else if o.Night && s.Status != session.Idle {
		c.lamp(x+92, y+70, 70, hex("#fbd678"), 0.16)
	}
	if s.Status == session.Waiting {
		bob := 0
		if o.Frame%2 == 1 {
			bob = -4
		}
		c.sprite(x+50, y+bob, spriteBubble, map[byte]color.RGBA{'o': cOutline, 'W': hex("#fff7e0"), 'A': hex("#b45309")}, 4)
	}

	look := looks[i%len(looks)]
	op := spriteOperator
	if s.Status == session.Idle || s.Status == session.Stale {
		op = spriteOperatorAsleep
	}
	c.sprite(x+36, y+36, op, map[byte]color.RGBA{
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
	c.sprite(x+104, y+46, mon, map[byte]color.RGBA{'o': cOutline, 'S': screenCol, 'L': lineCol}, 5)

	c.fill(x+12, y+86, x+12+168, y+86+20, cOutline)
	c.fill(x+16, y+90, x+16+160, y+90+12, th.deskTop)
	c.fill(x+20, y+106, x+20+152, y+106+28, cOutline)
	c.fill(x+24, y+106, x+24+144, y+106+24, th.desk)

	if flash {
		amber := hex("#fbbf24")
		c.fill(x+4, y-4, x+w-4, y, amber)
		c.fill(x+4, y+134, x+w-4, y+138, amber)
		c.fill(x+4, y-4, x+8, y+138, amber)
		c.fill(x+w-8, y-4, x+w-4, y+138, amber)
	}

	// Three lines in the narrow VT323 face, across the whole column:
	// project, session title, state.
	maxW := w - 16
	f := c.f.label
	c.text(f, x+12, y+140, c.fit(f, s.Project, maxW), cText, 0)
	line := y + 156
	if s.Title != "" && !o.HideTitles {
		c.text(f, x+12, line, c.fit(f, s.Title, maxW), cMuted, 0)
		line += 16
	}
	c.text(f, x+12, line, c.fit(f, label(s, o.Now), maxW), st.label, 0)
}

func label(s session.Session, now time.Time) string {
	switch s.Status {
	case session.Working:
		switch s.Tool {
		case "":
			return "réfléchit…"
		case "Task", "Agent": // waiting for a subagent
			return "sous-agent…"
		}
		return s.Tool
	case session.Waiting:
		what := "attend ton OK"
		switch s.Tool {
		case "AskUserQuestion":
			what = "te pose une question"
		case "ExitPlanMode":
			what = "plan à valider"
		}
		if d := now.Sub(s.Modified); d >= time.Minute {
			return what + " · " + ago(d)
		}
		return what
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

// BestScale picks the render scale and the integer upscale that fill a
// screen of w×h device pixels best (largest picture, square pixels).
func BestScale(w, h int) (scale float64, factor int) {
	best := 0
	scale, factor = 1, 1
	for _, s := range Scales {
		rw, rh := int(Width*s), int(Height*s)
		k := w / rw
		if hk := h / rh; hk < k {
			k = hk
		}
		if k >= 1 && rw*k >= best { // ties go to the finer scale
			best, scale, factor = rw*k, s, k
		}
	}
	return scale, factor
}

// pairing draws the code panel shown while a new machine pairs.
func (c *canvas) pairing(p collector.PairRequest, th theme) {
	c.dim(0, 60, Width, Height-32, th.bg, 0.7)
	w, h := 720, 340
	x0, y0 := (Width-w)/2, (Height-h)/2
	c.fill(x0, y0, x0+w, y0+h, cOutline)
	c.fill(x0+4, y0+4, x0+w-4, y0+h-4, hex("#fbbf24"))
	c.fill(x0+8, y0+8, x0+w-8, y0+h-8, th.wall)

	center := func(f *font, y int, s string, col color.RGBA, spacing int) {
		c.text(f, x0+(w-c.textWidth(f, s, spacing))/2, y, s, col, spacing)
	}
	center(c.f.small, y0+32, "NOUVELLE MACHINE", hex("#fbbf24"), 2)
	center(c.f.body, y0+64, c.fit(c.f.body, "Associer « "+p.Label+" » ?", w-64), cText, 0)

	// Code: "482 913", glyph pixels enlarged so it reads from the sofa.
	code := p.Code
	if len(code) == 6 {
		code = code[:3] + " " + code[3:]
	}
	// Regular Silkscreen: its digits stay unambiguous when enlarged
	// (the bold 4 reads as a blob).
	k := 4
	f := c.f.small
	sp := c.px(8)
	dw := f.width(code, 0)*k + sp*(len([]rune(code))-1)
	dh := f.height * k
	dx := c.px(x0) + (c.px(w)-dw)/2
	dy := c.px(y0+120) + (c.px(120)-dh)/2
	f.drawScaled(c.img, dx, dy, code, cText, sp, k)

	left := int(p.Left.Seconds())
	center(c.f.body, y0+h-72, "Tape ce code dans l'app Pixregie", cMuted, 0)
	center(c.f.body, y0+h-44, fmt.Sprintf("expire dans %d:%02d", left/60, left%60), cMuted, 0)
}
