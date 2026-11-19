package screen

import (
	"image"
	"image/color"
	"math"
)

// Sprites are rows of palette keys; '.' is transparent. Same art as the
// validated design canvas.
var (
	spriteOperator = []string{
		"....hhhh....",
		"...hhhhhh...",
		"..kkkkkkkk..",
		"..hssssssh..",
		".ksessssesk.",
		".kssssssssk.",
		"..kssddss...",
		"..cccccccc..",
		".cccccccccc.",
		".cccccccccc.",
	}
	spriteOperatorAsleep = []string{
		"....hhhh....",
		"...hhhhhh...",
		"..kkkkkkkk..",
		"..hssssssh..",
		".ksddssddsk.",
		".kssssssssk.",
		"..kssssss...",
		"..cccccccc..",
		".cccccccccc.",
		".cccccccccc.",
	}
	spriteMonitorA = []string{
		"oooooooooo",
		"oSSSSSSSSo",
		"oSLLLSSSSo",
		"oSSSLLLLSo",
		"oSLLSSSSSo",
		"oooooooooo",
		"....oo....",
		"..oooooo..",
	}
	spriteMonitorB = []string{
		"oooooooooo",
		"oSSSSSSSSo",
		"oSLLSSSSSo",
		"oSLLLSSSSo",
		"oSSSLLLLSo",
		"oooooooooo",
		"....oo....",
		"..oooooo..",
	}
	spriteMonitorAlert = []string{
		"oooooooooo",
		"oSSSSLLSSo",
		"oSSSSLLSSo",
		"oSSSSSSSSo",
		"oSSSSLLSSo",
		"oooooooooo",
		"....oo....",
		"..oooooo..",
	}
	spriteMonitorIdle = []string{
		"oooooooooo",
		"oSSSSSSSSo",
		"oSSSSSSSSo",
		"oSSSSSSSSo",
		"oSLSSSSSSo",
		"oooooooooo",
		"....oo....",
		"..oooooo..",
	}
	spriteBubble = []string{
		".ooooo.",
		"oWWWWWo",
		"oWWAWWo",
		"oWWAWWo",
		"oWWWWWo",
		"oWWAWWo",
		".ooooo.",
		"...o...",
	}
)

type look struct{ hair, skin color.RGBA }

var looks = []look{
	{hex("#2b2233"), hex("#f2c9a0")},
	{hex("#d9a441"), hex("#e8b48a")},
	{hex("#6b3e26"), hex("#8d5a3b")},
	{hex("#1c1a24"), hex("#c98b5e")},
}

func setPx(img *image.RGBA, x, y int, c color.RGBA) {
	if (image.Point{x, y}).In(img.Rect) {
		img.SetRGBA(x, y, c)
	}
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		i := img.PixOffset(r.Min.X, y)
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
			i += 4
		}
	}
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

// dim blends a rectangle toward c (offline rooms).
func dim(img *image.RGBA, r image.Rectangle, c color.RGBA, t float64) {
	r = r.Intersect(img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, mix(img.RGBAAt(x, y), c, t))
		}
	}
}

// lamp adds a soft pool of light, quantised in step-sized blocks to stay
// pixel-y.
func lamp(img *image.RGBA, cx, cy, radius, step int, c color.RGBA, strength float64) {
	for y := cy - radius; y < cy+radius; y += step {
		for x := cx - radius; x < cx+radius; x += step {
			d := math.Hypot(float64(x+step/2-cx), float64(y+step/2-cy)) / float64(radius)
			if d >= 1 {
				continue
			}
			t := strength * (1 - d)
			for py := y; py < y+step; py++ {
				for px := x; px < x+step; px++ {
					if (image.Point{px, py}).In(img.Rect) {
						img.SetRGBA(px, py, mix(img.RGBAAt(px, py), c, t))
					}
				}
			}
		}
	}
}
