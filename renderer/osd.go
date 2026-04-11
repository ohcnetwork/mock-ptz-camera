// osd.go implements the on-screen display (OSD) overlay for rendered frames.
//
// The OSD provides:
//   - DrawCrosshair: a white centre crosshair (2px thick, 40px span) used
//     by the test pattern renderer to indicate the camera centre point.
//   - DrawOSD: a top-left info panel showing the current timestamp, measured
//     FPS, and PTZ position (pan°, tilt°, zoom×). The panel has a darkened
//     background rectangle for readability and uses the bitmap font with
//     drop shadows.
//
// Both functions operate directly on RGB24 frame buffers using strided
// byte offsets for maximum performance.
package renderer

import (
	"fmt"
	"time"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// DrawCrosshair draws a white centre crosshair on an RGB24 frame buffer.
// The crosshair is 2 pixels thick and spans approximately 40 pixels in each
// direction from the frame centre. Used by TestRenderer for visual alignment.
func DrawCrosshair(buf []byte, w, h int) {
	cx, cy := w/2, h/2
	stride := w * 3

	// Horizontal line (2px thick).
	x0, x1 := cx-20, cx+21
	if x0 < 0 {
		x0 = 0
	}
	if x1 > w {
		x1 = w
	}
	for _, y := range [2]int{cy - 1, cy} {
		if y < 0 || y >= h {
			continue
		}
		off := y*stride + x0*3
		for x := x0; x < x1; x++ {
			buf[off] = 255
			buf[off+1] = 255
			buf[off+2] = 255
			off += 3
		}
	}

	// Vertical line (2px thick).
	y0, y1 := cy-20, cy+21
	if y0 < 0 {
		y0 = 0
	}
	if y1 > h {
		y1 = h
	}
	for _, x := range [2]int{cx - 1, cx} {
		if x < 0 || x >= w {
			continue
		}
		off := y0*stride + x*3
		for y := y0; y < y1; y++ {
			buf[off] = 255
			buf[off+1] = 255
			buf[off+2] = 255
			off += stride
		}
	}
}

// DrawOSD draws the on-screen display panel in the top-left corner.
//
// The panel includes:
//   - Current date/time (YYYY-MM-DD HH:MM:SS)
//   - Measured render FPS
//   - PTZ position: pan in degrees, tilt in degrees, zoom as multiplier
//
// A darkened background rectangle is drawn first for contrast, then each
// line is rendered using DrawTextShadow for additional readability.
func DrawOSD(buf []byte, w, h int, pos ptz.Position, fps float64) {
	now := time.Now()
	scale := 2
	lineH := (fontH + 2) * scale
	pad := 8

	lines := []string{
		now.Format("2006-01-02 15:04:05"),
		fmt.Sprintf("FPS:%.1f", fps),
		fmt.Sprintf("P:%.0f° T:%.0f° Z:%.0fx",
			pos.PanDeg(),
			pos.TiltDeg(),
			pos.ZoomX()),
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

	for i, line := range lines {
		DrawTextShadow(buf, w, h, pad, pad+i*lineH, line, scale, 0, 255, 0)
	}
}
