package renderer

import (
	"fmt"
	"image/color"
	"time"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// FlipVertical flips an RGB24 frame buffer vertically in-place.
func FlipVertical(buf []byte, w, h int) {
	stride := w * 3
	for top := 0; top < h/2; top++ {
		bot := h - 1 - top
		topOff := top * stride
		botOff := bot * stride
		for i := 0; i < stride; i++ {
			buf[topOff+i], buf[botOff+i] = buf[botOff+i], buf[topOff+i]
		}
	}
}

// DrawCrosshair draws a center crosshair and zoom bar on an RGB24 frame.
func DrawCrosshair(buf []byte, w, h int) {
	white := color.RGBA{255, 255, 255, 255}

	cy := h / 2
	for px := w/2 - 20; px <= w/2+20; px++ {
		if px >= 0 && px < w {
			setPixel(buf, w, px, cy, white)
			setPixel(buf, w, px, cy-1, white)
		}
	}
	cx := w / 2
	for py := h/2 - 20; py <= h/2+20; py++ {
		if py >= 0 && py < h {
			setPixel(buf, w, cx, py, white)
			setPixel(buf, w, cx-1, py, white)
		}
	}

	// Zoom bar at bottom
	barY := h - 12
	barX := w/2 - 50
	DarkenRect(buf, w, h, barX, barY-2, 101, 5)
}

// DrawOSD draws the on-screen display (timestamp, FPS, PTZ info) on an RGB24 frame.
func DrawOSD(buf []byte, w, h int, pos ptz.Position, fps float64) {
	now := time.Now()
	scale := 2
	lineH := (fontH + 2) * scale
	pad := 8

	lines := []string{
		now.Format("2006-01-02 15:04:05"),
		fmt.Sprintf("FPS:%.1f", fps),
		fmt.Sprintf("P:%+.1f° T:%+.1f° Z:%.1fx",
			pos.Pan*180.0,
			pos.Tilt*90.0,
			1.0+pos.Zoom*19.0),
	}
	if pos.Flipped {
		lines = append(lines, "AUTOFLIP")
	}

	maxChars := 0
	for _, l := range lines {
		if len(l) > maxChars {
			maxChars = len(l)
		}
	}
	bgW := maxChars*(fontW+1)*scale + pad*2
	bgH := len(lines)*lineH + pad*2
	DarkenRect(buf, w, h, 0, 0, bgW, bgH)

	green := color.RGBA{0, 255, 0, 255}
	for i, line := range lines {
		DrawTextShadow(buf, w, h, pad, pad+i*lineH, line, scale, green)
	}
}
