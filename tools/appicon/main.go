// appicon writes the app icon: the board's operator, headset on, cropped
// like a portrait on the control room's purple. One bold subject so it
// still reads at 16 px.
//
//	go run ./tools/appicon OUTDIR.iconset
//
// The iconset feeds iconutil (macOS) and go-winres (Windows exe).
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
)

// operator is 16×16; ear cups and the mic tip ('A') glow green.
var operator = []string{
	"....oooooooo....",
	"...ohhhhhhhho...",
	"..okkkkkkkkkko..",
	".okhhhhhhhhhhko.",
	".okohhhhhhhhoko.",
	".okossssssssoko.",
	"okkksssssssskkko",
	"okAkssessesskAko",
	"okAksssssssskAko",
	"okkksssssssskkko",
	".oooksssssssooo.",
	"...oskkAsssso...",
	"..ooossssssooo..",
	".occcccccccccco.",
	"occcccccccccccco",
	"occcccccccccccco",
}

var (
	background = hex(0x4e4670) // the board's desk purple
	palette    = map[byte]color.RGBA{
		'o': hex(0x0c0a12), 'k': hex(0x0c0a12), 'e': hex(0x0c0a12),
		'h': hex(0x6b3e26), 's': hex(0xf2c9a0),
		'A': hex(0x4ade80), 'c': hex(0x6ea8ff),
	}
)

func hex(v uint32) color.RGBA {
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: appicon OUTDIR.iconset")
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, size := range []int{16, 32, 64, 128, 256, 512, 1024} {
		img := render(size)
		write(filepath.Join(dir, fmt.Sprintf("icon_%dx%d.png", size, size)), img)
		if size > 16 {
			write(filepath.Join(dir, fmt.Sprintf("icon_%dx%d@2x.png", size/2, size/2)), img)
		}
	}
}

// render: rounded tile (macOS keeps ~10 % margin around it), the operator
// at the largest whole-pixel scale that leaves room above the head,
// standing on the tile's bottom edge.
func render(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	m := size / 10
	if size <= 32 {
		m = 0
	}
	tile := image.Rect(m, m, size-m, size-m)
	r := tile.Dx() / 5
	n := len(operator)
	scale := tile.Dx() * 13 / 16 / n
	if scale*n*10 < tile.Dx()*7 { // tiny sizes: fill the tile instead
		scale = max(1, tile.Dx()/n)
	}
	ox := tile.Min.X + (tile.Dx()-n*scale)/2
	oy := tile.Max.Y - n*scale
	for y := tile.Min.Y; y < tile.Max.Y; y++ {
		for x := tile.Min.X; x < tile.Max.X; x++ {
			if !inRounded(x-tile.Min.X, y-tile.Min.Y, tile.Dx(), r) {
				continue
			}
			c := background
			if sx, sy := (x-ox)/scale, (y-oy)/scale; x >= ox && y >= oy && sx < n && sy < n {
				if p, ok := palette[operator[sy][sx]]; ok {
					c = p
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func inRounded(x, y, size, r int) bool {
	cx, cy := 0, 0
	switch {
	case x < r && y < r:
		cx, cy = r-x, r-y
	case x >= size-r && y < r:
		cx, cy = x-(size-r-1), r-y
	case x < r && y >= size-r:
		cx, cy = r-x, y-(size-r-1)
	case x >= size-r && y >= size-r:
		cx, cy = x-(size-r-1), y-(size-r-1)
	}
	return cx*cx+cy*cy <= r*r
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
