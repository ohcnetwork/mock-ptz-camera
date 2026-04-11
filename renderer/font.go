// font.go provides a minimal 5×7 bitmap font for rendering text directly
// onto RGB24 frame buffers without any external font dependencies.
//
// Each character is stored as 7 rows of bit-packed data where bit 4 is the
// leftmost pixel. Characters can be rendered at arbitrary integer scale
// factors (e.g. scale=2 renders each font pixel as a 2×2 block).
//
// Functions:
//   - DrawText:       renders plain text at a given position and colour.
//   - DrawTextShadow: renders text with a 1-pixel black offset shadow
//                     for readability on variable backgrounds.
//   - DarkenRect:     dims a rectangular region by halving each colour
//                     channel (right-shift by 1) to create a translucent
//                     dark overlay for OSD backgrounds.
package renderer

const (
	fontW = 5 // glyph width in pixels
	fontH = 7 // glyph height in pixels
)

// glyphs stores the 5×7 bitmap for each ASCII character. Each [fontH]byte
// array represents 7 rows, where bits 4..0 map to pixels left-to-right.
var glyphs [128][fontH]byte

// init populates the bitmap font glyphs for printable ASCII characters.
func init() {
	define := func(ch byte, rows [fontH]byte) { glyphs[ch] = rows }

	define(' ', [7]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	define('!', [7]byte{0x04, 0x04, 0x04, 0x04, 0x04, 0x00, 0x04})
	define('+', [7]byte{0x00, 0x04, 0x04, 0x1F, 0x04, 0x04, 0x00})
	define('-', [7]byte{0x00, 0x00, 0x00, 0x1F, 0x00, 0x00, 0x00})
	define('.', [7]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x06})
	define('/', [7]byte{0x01, 0x01, 0x02, 0x04, 0x08, 0x10, 0x10})
	define(':', [7]byte{0x00, 0x06, 0x06, 0x00, 0x06, 0x06, 0x00})

	define('0', [7]byte{0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E})
	define('1', [7]byte{0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E})
	define('2', [7]byte{0x0E, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1F})
	define('3', [7]byte{0x1F, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0E})
	define('4', [7]byte{0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02})
	define('5', [7]byte{0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E})
	define('6', [7]byte{0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E})
	define('7', [7]byte{0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08})
	define('8', [7]byte{0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E})
	define('9', [7]byte{0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C})

	define('A', [7]byte{0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11})
	define('B', [7]byte{0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E})
	define('C', [7]byte{0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E})
	define('D', [7]byte{0x1C, 0x12, 0x11, 0x11, 0x11, 0x12, 0x1C})
	define('E', [7]byte{0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F})
	define('F', [7]byte{0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10})
	define('G', [7]byte{0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F})
	define('H', [7]byte{0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11})
	define('I', [7]byte{0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E})
	define('J', [7]byte{0x07, 0x02, 0x02, 0x02, 0x02, 0x12, 0x0C})
	define('K', [7]byte{0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11})
	define('L', [7]byte{0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F})
	define('M', [7]byte{0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11})
	define('N', [7]byte{0x11, 0x11, 0x19, 0x15, 0x13, 0x11, 0x11})
	define('O', [7]byte{0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E})
	define('P', [7]byte{0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10})
	define('Q', [7]byte{0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D})
	define('R', [7]byte{0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11})
	define('S', [7]byte{0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E})
	define('T', [7]byte{0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04})
	define('U', [7]byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E})
	define('V', [7]byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04})
	define('W', [7]byte{0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0A})
	define('X', [7]byte{0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11})
	define('Y', [7]byte{0x11, 0x11, 0x11, 0x0A, 0x04, 0x04, 0x04})
	define('Z', [7]byte{0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F})
}

// DrawText renders a string onto an RGB24 frame buffer at position (x, y).
// Each glyph is scaled by `scale` (e.g. scale=2 doubles the size).
// Characters are spaced at (fontW+1)*scale pixels apart (5px glyph + 1px gap).
func DrawText(frame []byte, width, height, x, y int, text string, scale int, r, g, b byte) {
	charW := (fontW + 1) * scale // 5 pixels + 1 gap, scaled
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch >= 128 {
			ch = '?'
		}
		drawGlyph(frame, width, height, x+i*charW, y, glyphs[ch], scale, r, g, b)
	}
}

// drawGlyph renders a single character bitmap onto the frame buffer.
// It uses direct strided writes to avoid per-pixel coordinate calculations.
// Pixels outside the frame bounds are safely clipped by clamping the
// sub-pixel iteration ranges.
func drawGlyph(frame []byte, width, height, gx, gy int, glyph [fontH]byte, scale int, r, g, b byte) {
	glyphPxW := fontW * scale
	glyphPxH := fontH * scale

	// Quick reject: entirely outside frame.
	if gx+glyphPxW <= 0 || gx >= width || gy+glyphPxH <= 0 || gy >= height {
		return
	}

	stride := width * 3

	for row := 0; row < fontH; row++ {
		bits := glyph[row]
		if bits == 0 {
			continue
		}
		baseY := gy + row*scale
		if baseY+scale <= 0 || baseY >= height {
			continue
		}
		// Clamp Y sub-pixel range.
		syStart := 0
		if baseY < 0 {
			syStart = -baseY
		}
		syEnd := scale
		if baseY+syEnd > height {
			syEnd = height - baseY
		}

		for col := 0; col < fontW; col++ {
			if bits&(1<<uint(4-col)) == 0 {
				continue
			}
			baseX := gx + col*scale
			if baseX+scale <= 0 || baseX >= width {
				continue
			}
			// Clamp X sub-pixel range.
			sxStart := 0
			if baseX < 0 {
				sxStart = -baseX
			}
			sxEnd := scale
			if baseX+sxEnd > width {
				sxEnd = width - baseX
			}

			spanW := sxEnd - sxStart
			for sy := syStart; sy < syEnd; sy++ {
				off := (baseY+sy)*stride + (baseX+sxStart)*3
				for sx := 0; sx < spanW; sx++ {
					frame[off] = r
					frame[off+1] = g
					frame[off+2] = b
					off += 3
				}
			}
		}
	}
}

// DrawTextShadow renders text with a 1-pixel black drop shadow for readability.
// The shadow is drawn first at offset (+1, +1), then the foreground text
// is drawn on top. This provides good contrast on any background colour.
func DrawTextShadow(frame []byte, width, height, x, y int, text string, scale int, r, g, b byte) {
	charW := (fontW + 1) * scale
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch >= 128 {
			ch = '?'
		}
		glyph := glyphs[ch]
		drawGlyph(frame, width, height, x+1+i*charW, y+1, glyph, scale, 0, 0, 0)
		drawGlyph(frame, width, height, x+i*charW, y, glyph, scale, r, g, b)
	}
}

// DarkenRect dims pixels in the given rectangle by halving each colour channel.
// This creates a semi-transparent dark overlay effect used as a background
// for OSD text to ensure readability regardless of the underlying image.
// The rectangle is clamped to the frame bounds.
func DarkenRect(frame []byte, width, height, rx, ry, rw, rh int) {
	// Pre-clamp bounds.
	x0, y0 := rx, ry
	x1, y1 := rx+rw, ry+rh
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}

	stride := width * 3
	spanBytes := (x1 - x0) * 3
	for py := y0; py < y1; py++ {
		off := py*stride + x0*3
		end := off + spanBytes
		for i := off; i < end; i++ {
			frame[i] >>= 1
		}
	}
}
