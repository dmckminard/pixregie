// appicon writes the app icon: the control room's ON AIR sign, the same as
// the menu bar icon, lit studio red. Drawn on a 32×32 grid (the 16×16 sign
// at twice the size); the 16 px icon uses the menu bar sign itself.
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

var big = []string{
	"..oooooooooooooooooooooooooooo..",
	".oooooooooooooooooooooooooooooo.",
	"oooooooooooooooooooooooooooooooo",
	"ooSSSSSSSSSSSSSSSSSSSSSSSSSSSSoo",
	"ooSSSSSSSSSSSSSSSSSSSSSSSSSSSSoo",
	"ooSSSSSSLLLLLLSSLLSSSSLLSSSSSSoo",
	"ooSSSSSSLLLLLLSSLLSSSSLLSSSSSSoo",
	"ooSSSSSSLLSSLLSSLLLLSSLLSSSSSSoo",
	"ooSSSSSSLLSSLLSSLLLLSSLLSSSSSSoo",
	"ooSSSSSSLLSSLLSSLLSSLLLLSSSSSSoo",
	"ooSSSSSSLLSSLLSSLLSSLLLLSSSSSSoo",
	"ooSSSSSSLLSSLLSSLLSSSSLLSSSSSSoo",
	"ooSSSSSSLLSSLLSSLLSSSSLLSSSSSSoo",
	"ooSSSSSSLLLLLLSSLLSSSSLLSSSSSSoo",
	"ooSSSSSSLLLLLLSSLLSSSSLLSSSSSSoo",
	"ooSSSSSSSSSSSSSSSSSSSSSSSSSSSSoo",
	"ooSSSSSSSSSSSSSSSSSSSSSSSSSSSSoo",
	"ooSSSSLLSSSSLLLLLLSSLLLLSSSSSSoo",
	"ooSSSSLLSSSSLLLLLLSSLLLLSSSSSSoo",
	"ooSSLLSSLLSSSSLLSSSSLLSSLLSSSSoo",
	"ooSSLLSSLLSSSSLLSSSSLLSSLLSSSSoo",
	"ooSSLLLLLLSSSSLLSSSSLLLLSSSSSSoo",
	"ooSSLLLLLLSSSSLLSSSSLLLLSSSSSSoo",
	"ooSSLLSSLLSSSSLLSSSSLLSSLLSSSSoo",
	"ooSSLLSSLLSSSSLLSSSSLLSSLLSSSSoo",
	"ooSSLLSSLLSSLLLLLLSSLLSSLLSSSSoo",
	"ooSSLLSSLLSSLLLLLLSSLLSSLLSSSSoo",
	"ooSSSSSSSSSSSSSSSSSSSSSSSSSSSSoo",
	"ooSSSSSSSSSSSSSSSSSSSSSSSSSSSSoo",
	"oooooooooooooooooooooooooooooooo",
	".oooooooooooooooooooooooooooooo.",
	"..oooooooooooooooooooooooooooo..",
}

// small is the menu bar sign (internal/icon), for 16 px.
var small = []string{
	".oooooooooooooo.",
	"oSSSSSSSSSSSSSSo",
	"oSSSLLLSLSSLSSSo",
	"oSSSLSLSLLSLSSSo",
	"oSSSLSLSLSLLSSSo",
	"oSSSLSLSLSSLSSSo",
	"oSSSLLLSLSSLSSSo",
	"oSSSSSSSSSSSSSSo",
	"oSSLSSLLLSLLSSSo",
	"oSLSLSSLSSLSLSSo",
	"oSLLLSSLSSLLSSSo",
	"oSLSLSSLSSLSLSSo",
	"oSLSLSLLLSLSLSSo",
	"oSSSSSSSSSSSSSSo",
	".oooooooooooooo.",
	"................",
}

var palette = map[byte]color.RGBA{
	'o': hex(0x0c0a12), // frame
	'L': hex(0x0c0a12), // letters
	'S': hex(0xf87171), // lit panel
}

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

// render draws the art at the largest whole-pixel scale, centred. macOS
// icons keep a margin around the artwork (Apple's grid: about 10 %).
func render(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	art := big
	m := size / 10
	if size < 32 {
		art, m = small, 0
	} else if size == 32 {
		m = 0
	}
	n := len(art)
	scale := max(1, (size-2*m)/n)
	if scale*n*10 < size*7 { // the margin would halve a small icon
		scale = size / n
	}
	off := (size - n*scale) / 2
	for y, row := range art {
		for x := 0; x < len(row); x++ {
			c, ok := palette[row[x]]
			if !ok {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetRGBA(off+x*scale+dx, off+y*scale+dy, c)
				}
			}
		}
	}
	return img
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
