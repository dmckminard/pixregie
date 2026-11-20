// Package icon draws the tray icon: the control room's ON AIR sign, lit in
// the colour of the most urgent session, dark when everything is idle.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"

	"github.com/dmckminard/pixregie/internal/session"
)

// sign fills the whole 16×16 square: macOS draws every status icon at
// 16×16 pt, so empty rows would make it look smaller than its neighbours.
// 'o' frame, 'S' panel (state colour), 'L' letters.
var sign = []string{
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

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

// Disconnected draws the idle outline with a dimmed frame.
const Disconnected session.Status = "disconnected"

// Image returns the icon at 16×scale pixels. outline is the frame colour:
// light for dark menu bars, dark for light ones; macOS template icons use
// black and let the system recolour them.
func Image(st session.Status, scale int, outline color.RGBA) *image.RGBA {
	// Unlit (idle, disconnected): frame and letters in the menu bar's
	// colour, panel transparent.
	rows, screen, line := sign, color.RGBA{}, outline
	switch st {
	case session.Working:
		screen, line = rgb(0x4a, 0xde, 0x80), rgb(0x0c, 0x0a, 0x12)
	case session.Waiting:
		screen, line = rgb(0xfb, 0xbf, 0x24), rgb(0x0c, 0x0a, 0x12)
	case session.Stale:
		screen, line = rgb(0xf8, 0x71, 0x71), rgb(0x0c, 0x0a, 0x12)
	case Disconnected:
		outline.A = 110
		line = outline
	}
	img := image.NewRGBA(image.Rect(0, 0, 16*scale, 16*scale))
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			var c color.RGBA
			switch row[x] {
			case 'o':
				c = outline
			case 'S':
				c = screen
				if c.A == 0 {
					continue
				}
			case 'L':
				c = line
			default:
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetRGBA(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	return img
}

// PNG encodes Image.
func PNG(st session.Status, scale int, outline color.RGBA) []byte {
	var b bytes.Buffer
	png.Encode(&b, Image(st, scale, outline))
	return b.Bytes()
}

// ICO encodes img as a Windows .ico with one classic 32-bit BGRA bitmap
// entry (plus the 1-bit AND mask). PNG-compressed entries are only read
// reliably at 256 px; small tray icons must be bitmaps.
func ICO(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	maskStride := ((w + 31) / 32) * 4
	var dib bytes.Buffer
	// BITMAPINFOHEADER: height counts the colour rows and the mask rows.
	binary.Write(&dib, binary.LittleEndian, struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}{40, int32(w), int32(2 * h), 1, 32, 0, uint32(w*h*4 + maskStride*h), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- { // bottom-up
		for x := 0; x < w; x++ {
			c := img.RGBAAt(img.Rect.Min.X+x, img.Rect.Min.Y+y)
			dib.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	for y := h - 1; y >= 0; y-- {
		row := make([]byte, maskStride)
		for x := 0; x < w; x++ {
			if img.RGBAAt(img.Rect.Min.X+x, img.Rect.Min.Y+y).A == 0 {
				row[x/8] |= 0x80 >> (x % 8)
			}
		}
		dib.Write(row)
	}

	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint16{0, 1, 1}) // reserved, icon, 1 image
	dim := func(n int) byte {
		if n >= 256 {
			return 0
		}
		return byte(n)
	}
	b.Write([]byte{dim(w), dim(h), 0, 0})
	binary.Write(&b, binary.LittleEndian, []uint16{1, 32})
	binary.Write(&b, binary.LittleEndian, []uint32{uint32(dib.Len()), 6 + 16})
	b.Write(dib.Bytes())
	return b.Bytes()
}
