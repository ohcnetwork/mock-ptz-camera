package renderer

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// TestRenderer generates a test pattern frame with PTZ simulation.
type TestRenderer struct {
	width, height int
	buf           []byte
}

func NewTestRenderer(width, height int) *TestRenderer {
	return &TestRenderer{
		width:  width,
		height: height,
		buf:    make([]byte, width*height*3),
	}
}

// Render generates a test pattern frame based on the PTZ position.
func (t *TestRenderer) Render(pos ptz.Position, fps float64) []byte {
	t.renderPattern(pos)
	if pos.Flipped {
		t.flipVertical()
	}
	t.drawCrosshair()
	t.drawOSD(pos, fps)
	return t.buf
}

func (t *TestRenderer) renderPattern(pos ptz.Position) {
	w, h := t.width, t.height

	// Map PTZ pan/tilt to a virtual camera offset in a large tiled plane.
	gridScale := 40.0 / (1.0 + pos.Zoom*9.0)
	invGrid := 1.0 / gridScale
	offsetX := pos.Pan * 500.0
	offsetY := -pos.Tilt * 500.0
	halfW := float64(w) / 2
	halfH := float64(h) / 2
	lineThresh := 0.04

	for py := 0; py < h; py++ {
		wy := float64(py) - halfH + offsetY
		gyRaw := wy * invGrid
		gy := math.Floor(gyRaw)
		fracY := gyRaw - gy

		rowIdx := py * w * 3
		for px := 0; px < w; px++ {
			wx := float64(px) - halfW + offsetX
			gxRaw := wx * invGrid
			gx := math.Floor(gxRaw)
			fracX := gxRaw - gx

			var r, g, b uint8

			// Check grid lines first (cheapest branch)
			onLine := fracX < lineThresh || fracY < lineThresh ||
				(1-fracX) < lineThresh || (1-fracY) < lineThresh

			if onLine {
				r, g, b = 60, 60, 60
			} else {
				// Simple checkerboard with subtle color variation
				igx := int(gx)
				igy := int(gy)
				if (igx+igy)&1 == 0 {
					r, g, b = 180, 200, 220
				} else {
					r, g, b = 140, 150, 165
				}
				// Tint cells by quadrant
				if igx >= 0 {
					r += 30
				}
				if igy >= 0 {
					b += 30
				}
			}

			// Major axes
			absWy := wy
			if absWy < 0 {
				absWy = -absWy
			}
			absWx := wx
			if absWx < 0 {
				absWx = -absWx
			}
			axisThreshold := gridScale * 0.08
			if absWy < axisThreshold {
				r, g, b = 200, 50, 50
			}
			if absWx < axisThreshold {
				r, g, b = 50, 50, 200
			}

			idx := rowIdx + px*3
			t.buf[idx] = r
			t.buf[idx+1] = g
			t.buf[idx+2] = b
		}
	}
}

func (t *TestRenderer) flipVertical() {
	w, h := t.width, t.height
	stride := w * 3
	for top := 0; top < h/2; top++ {
		bot := h - 1 - top
		topOff := top * stride
		botOff := bot * stride
		for i := 0; i < stride; i++ {
			t.buf[topOff+i], t.buf[botOff+i] = t.buf[botOff+i], t.buf[topOff+i]
		}
	}
}

func (t *TestRenderer) drawCrosshair() {
	w, h := t.width, t.height
	white := color.RGBA{255, 255, 255, 255}

	cy := h / 2
	for px := w/2 - 20; px <= w/2+20; px++ {
		if px >= 0 && px < w {
			setPixel(t.buf, w, px, cy, white)
			setPixel(t.buf, w, px, cy-1, white)
		}
	}
	cx := w / 2
	for py := h/2 - 20; py <= h/2+20; py++ {
		if py >= 0 && py < h {
			setPixel(t.buf, w, cx, py, white)
			setPixel(t.buf, w, cx-1, py, white)
		}
	}

	// Zoom bar at bottom
	barY := h - 12
	barX := w/2 - 50
	DarkenRect(t.buf, w, h, barX, barY-2, 101, 5)
}

func (t *TestRenderer) drawOSD(pos ptz.Position, fps float64) {
	w, h := t.width, t.height
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
	DarkenRect(t.buf, w, h, 0, 0, bgW, bgH)

	green := color.RGBA{0, 255, 0, 255}
	for i, line := range lines {
		DrawTextShadow(t.buf, w, h, pad, pad+i*lineH, line, scale, green)
	}
}

func setPixel(frame []byte, width, x, y int, c color.RGBA) {
	idx := (y*width + x) * 3
	if idx >= 0 && idx+2 < len(frame) {
		frame[idx], frame[idx+1], frame[idx+2] = c.R, c.G, c.B
	}
}
