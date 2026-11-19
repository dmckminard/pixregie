package screen

import (
	"image"
	"image/color"
	"math"
)

// The scene is laid out in logical units on a 1280×720 board (the design
// canvas). A canvas maps those units to device pixels with a scale factor:
// 1 for 1280×720, 1.5 for native 1920×1080. Sprites and fonts are chosen so
// that every drawn pixel stays square and crisp at either scale.
const (
	Width  = 1280 // logical width
	Height = 720  // logical height
)

// Scales the renderer supports. Larger screens get an integer upscale of one
// of these from the framebuffer writer.
var Scales = []float64{1, 1.5}

type fontSet struct{ small, title, body, label *font }

var fontSets = map[float64]fontSet{
	1:   {silk16, silk24b, vt20, mustFont("vt16")},
	1.5: {mustFont("silk24"), mustFont("silk32b"), mustFont("vt30"), mustFont("vt24")},
}

type canvas struct {
	img *image.RGBA
	u   float64
	f   fontSet
}

func newCanvas(u float64) *canvas {
	fs, ok := fontSets[u]
	if !ok {
		u, fs = 1, fontSets[1]
	}
	w, h := int(Width*u), int(Height*u)
	return &canvas{img: image.NewRGBA(image.Rect(0, 0, w, h)), u: u, f: fs}
}

// px converts a logical coordinate to device pixels.
func (c *canvas) px(v int) int { return int(math.Round(float64(v) * c.u)) }

func (c *canvas) rect(x0, y0, x1, y1 int) image.Rectangle {
	return image.Rect(c.px(x0), c.px(y0), c.px(x1), c.px(y1))
}

func (c *canvas) fill(x0, y0, x1, y1 int, col color.RGBA) {
	fill(c.img, c.rect(x0, y0, x1, y1), col)
}

// text draws s at logical (x, y); spacing is logical. Returns logical width.
func (c *canvas) text(f *font, x, y int, s string, col color.RGBA, spacing int) int {
	w := f.draw(c.img, c.px(x), c.px(y), s, col, c.px(spacing))
	return int(math.Ceil(float64(w) / c.u))
}

func (c *canvas) textWidth(f *font, s string, spacing int) int {
	return int(math.Ceil(float64(f.width(s, c.px(spacing))) / c.u))
}

func (c *canvas) textHeight(f *font) int { return int(math.Ceil(float64(f.height) / c.u)) }

func (c *canvas) fit(f *font, s string, max int) string { return f.fit(s, c.px(max)) }

// sprite draws pixel art with a logical pixel size p. The device pixel size
// is rounded down so every sprite pixel has the same size.
func (c *canvas) sprite(x, y int, rows []string, pal map[byte]color.RGBA, p int) {
	dp := int(float64(p) * c.u)
	ox, oy := c.px(x), c.px(y)
	for sy, row := range rows {
		for sx := 0; sx < len(row); sx++ {
			col, ok := pal[row[sx]]
			if !ok {
				continue
			}
			fill(c.img, image.Rect(ox+sx*dp, oy+sy*dp, ox+sx*dp+dp, oy+sy*dp+dp), col)
		}
	}
}

func (c *canvas) dim(x0, y0, x1, y1 int, col color.RGBA, t float64) {
	dim(c.img, c.rect(x0, y0, x1, y1), col, t)
}

func (c *canvas) lamp(cx, cy, r int, col color.RGBA, strength float64) {
	lamp(c.img, c.px(cx), c.px(cy), c.px(r), c.px(4), col, strength)
}
