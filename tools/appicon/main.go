// appicon writes the macOS .iconset (pixel-art monitor on the night
// background) used to build Pixregie.app's icon with iconutil.
//
//	go run ./tools/appicon OUTDIR.iconset
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/dmckminard/pixregie/internal/icon"
	"github.com/dmckminard/pixregie/internal/session"
)

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

// render: rounded night square, monitor sprite centred, integer-scaled.
func render(size int) *image.RGBA {
	bg := color.RGBA{0x14, 0x12, 0x1c, 0xff}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	r := size / 5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if inRounded(x, y, size, r) {
				img.SetRGBA(x, y, bg)
			}
		}
	}
	scale := size * 3 / 4 / 16
	if scale < 1 {
		scale = 1
	}
	spr := icon.Image(session.Working, scale, color.RGBA{0xe8, 0xe4, 0xf4, 0xff})
	off := (size - 16*scale) / 2
	for y := 0; y < spr.Rect.Dy(); y++ {
		for x := 0; x < spr.Rect.Dx(); x++ {
			if c := spr.RGBAAt(x, y); c.A > 0 {
				img.SetRGBA(off+x, off+y+scale, c)
			}
		}
	}
	return img
}

func inRounded(x, y, size, r int) bool {
	cx, cy := x, y
	switch {
	case x < r && y < r:
		cx, cy = r-x, r-y
	case x >= size-r && y < r:
		cx, cy = x-(size-r-1), r-y
	case x < r && y >= size-r:
		cx, cy = r-x, y-(size-r-1)
	case x >= size-r && y >= size-r:
		cx, cy = x-(size-r-1), y-(size-r-1)
	default:
		return true
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
