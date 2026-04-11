// testpattern.go provides a synthetic test-pattern renderer for development
// and testing without requiring a panoramic source image.
//
// The test pattern consists of:
//   - A checkerboard grid that responds to pan/tilt (viewport offset) and
//     zoom (grid scale), simulating PTZ camera movement.
//   - Colour-tinted quadrants for orientation reference.
//   - Red horizontal and blue vertical axis lines through the origin.
//   - A centre crosshair overlay and OSD info panel.
//
// TestRenderer implements the Renderer interface and can be used as a
// drop-in replacement for PanoRenderer when no panoramic image is available.
package renderer

import (
	"math"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// TestRenderer generates synthetic test pattern frames for development.
// It implements the Renderer interface and responds to PTZ input by shifting
// the viewport offset and adjusting the grid scale.
type TestRenderer struct {
	width, height int    // output frame dimensions
	buf           []byte // reusable RGB24 frame buffer
}

// NewTestRenderer creates a test pattern renderer for the given output dimensions.
func NewTestRenderer(width, height int) *TestRenderer {
	return &TestRenderer{
		width:  width,
		height: height,
		buf:    make([]byte, width*height*3),
	}
}

// Render produces a test pattern frame with crosshair and OSD overlay.
// The pattern includes a grid, axis lines, and quadrant tinting that all
// respond to the current PTZ position.
func (t *TestRenderer) Render(pos ptz.Position, fps float64) []byte {
	t.renderPattern(pos)
	DrawCrosshair(t.buf, t.width, t.height)
	DrawOSD(t.buf, t.width, t.height, pos, fps)
	return t.buf
}

// renderPattern generates the checkerboard test grid with axis lines.
// The grid scale increases with zoom, and the viewport is offset by
// pan (horizontal) and tilt (vertical) to simulate camera movement.
func (t *TestRenderer) renderPattern(pos ptz.Position) {
	w, h := t.width, t.height

	// Map PTZ pan/tilt to a virtual camera offset in a large tiled plane.
	gridScale := 40.0 * (1.0 + pos.Zoom*9.0)
	invGrid := 1.0 / gridScale
	offsetX := pos.Pan * 500.0
	offsetY := -(pos.Tilt - ptz.TiltHorizon) * 500.0
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
