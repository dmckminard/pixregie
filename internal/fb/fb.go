// Package fb writes frames to a Linux framebuffer (/dev/fb0) without cgo,
// mmap or ioctls: geometry comes from sysfs and pixels go through pwrite.
// Supported formats: 16 bpp RGB565 and 32 bpp XRGB8888, the two the
// Raspberry Pi KMS driver exposes.
package fb

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Device struct {
	f         *os.File
	W, H, BPP int
	stride    int
	buf       []byte
}

// Open opens e.g. "/dev/fb0", reading its geometry from /sys/class/graphics.
func Open(dev string) (*Device, error) {
	sys := filepath.Join("/sys/class/graphics", filepath.Base(dev))
	size, err := readSys(sys, "virtual_size")
	if err != nil {
		return nil, err
	}
	wh := strings.Split(size, ",")
	if len(wh) != 2 {
		return nil, fmt.Errorf("fb: odd virtual_size %q", size)
	}
	w, _ := strconv.Atoi(wh[0])
	h, _ := strconv.Atoi(wh[1])
	bpps, err := readSys(sys, "bits_per_pixel")
	if err != nil {
		return nil, err
	}
	bpp, _ := strconv.Atoi(bpps)
	stride := w * bpp / 8
	if s, err := readSys(sys, "stride"); err == nil {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			stride = v
		}
	}
	if bpp != 16 && bpp != 32 {
		return nil, fmt.Errorf("fb: unsupported %d bpp", bpp)
	}
	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	return &Device{f: f, W: w, H: h, BPP: bpp, stride: stride, buf: make([]byte, stride*h)}, nil
}

func readSys(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b)), err
}

func (d *Device) Close() error { return d.f.Close() }

// Show scales src to the screen with nearest-neighbour (integer factor when
// possible, letterboxed in black) and writes it in one pwrite.
func (d *Device) Show(src *image.RGBA) error {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	// Largest scale that fits, integer if >= 1 so pixels stay square.
	scale := float64(d.W) / float64(sw)
	if s := float64(d.H) / float64(sh); s < scale {
		scale = s
	}
	if scale >= 1 {
		scale = float64(int(scale))
	}
	dw, dh := int(float64(sw)*scale), int(float64(sh)*scale)
	ox, oy := (d.W-dw)/2, (d.H-dh)/2

	clear(d.buf)
	for y := 0; y < dh; y++ {
		sy := int(float64(y) / scale)
		row := d.buf[(oy+y)*d.stride:]
		for x := 0; x < dw; x++ {
			sx := int(float64(x) / scale)
			i := src.PixOffset(sx, sy)
			r, g, b := src.Pix[i], src.Pix[i+1], src.Pix[i+2]
			px := ox + x
			if d.BPP == 32 {
				o := px * 4
				row[o], row[o+1], row[o+2], row[o+3] = b, g, r, 0xff // XRGB8888, little-endian
			} else {
				v := uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
				o := px * 2
				row[o], row[o+1] = byte(v), byte(v>>8)
			}
		}
	}
	_, err := d.f.WriteAt(d.buf, 0)
	return err
}
