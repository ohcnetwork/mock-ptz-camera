package renderer

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// PanoRenderer renders a perspective view projected from an equirectangular
// 360° panoramic image, simulating a PTZ camera navigating the sphere.
type PanoRenderer struct {
	width, height int
	buf           []byte

	// Source equirectangular image stored as RGB24.
	srcRGB     []byte
	srcW, srcH int
}

// NewPanoRenderer loads an equirectangular image and returns a renderer that
// projects a virtual perspective camera into it based on PTZ state.
func NewPanoRenderer(width, height int, imagePath string) (*PanoRenderer, error) {
	srcRGB, srcW, srcH, err := loadImageRGB(imagePath)
	if err != nil {
		return nil, fmt.Errorf("pano: load image %q: %w", imagePath, err)
	}
	log.WithFields(log.Fields{
		"path": imagePath, "src_width": srcW, "src_height": srcH,
	}).Info("loaded panoramic image")

	return &PanoRenderer{
		width:  width,
		height: height,
		buf:    make([]byte, width*height*3),
		srcRGB: srcRGB,
		srcW:   srcW,
		srcH:   srcH,
	}, nil
}

// Render produces an RGB24 frame showing the perspective projection into the
// panoramic sphere at the given PTZ position.
func (p *PanoRenderer) Render(pos ptz.Position, fps float64) []byte {
	w, h := p.width, p.height

	// Map PTZ values to camera orientation.
	// Pan: -1..+1 → yaw -π..+π (full 360°)
	yaw := pos.Pan * math.Pi
	// Tilt: -0.33..+1.0 → pitch (negate so positive tilt looks up)
	pitch := -pos.Tilt * math.Pi / 2.0
	// Zoom: 0..1 → horizontal FOV from 90° down to ~4.5°
	fovH := (math.Pi / 2.0) / (1.0 + pos.Zoom*19.0)

	// Precompute rotation sin/cos.
	sinY, cosY := math.Sincos(yaw)
	sinP, cosP := math.Sincos(pitch)

	// Half-dimensions for NDC mapping.
	halfW := float64(w) / 2.0
	halfH := float64(h) / 2.0
	aspect := float64(w) / float64(h)
	focalLen := halfW / math.Tan(fovH/2.0)

	srcWf := float64(p.srcW)
	srcHf := float64(p.srcH)

	for py := 0; py < h; py++ {
		// Camera-space y: positive up.
		cy := halfH - float64(py) - 0.5
		rowIdx := py * w * 3

		for px := 0; px < w; px++ {
			cx := float64(px) - halfW + 0.5
			_ = aspect // aspect is baked into pixel coordinates

			// Ray direction in camera space (looking along +Z).
			dx := cx
			dy := cy
			dz := focalLen

			// Normalize.
			invLen := 1.0 / math.Sqrt(dx*dx+dy*dy+dz*dz)
			dx *= invLen
			dy *= invLen
			dz *= invLen

			// Rotate by pitch (around X axis).
			dy2 := dy*cosP - dz*sinP
			dz2 := dy*sinP + dz*cosP

			// Rotate by yaw (around Y axis).
			dx3 := dx*cosY + dz2*sinY
			dz3 := -dx*sinY + dz2*cosY
			dy3 := dy2

			// Convert to spherical coordinates → equirectangular UV.
			theta := math.Atan2(dx3, dz3)       // azimuth: -π..+π
			phi := math.Asin(clamp(dy3, -1, 1)) // elevation: -π/2..+π/2

			// Map to source image pixel coordinates.
			u := (theta/(2*math.Pi) + 0.5) * srcWf // 0..srcW
			v := (0.5 - phi/math.Pi) * srcHf       // 0..srcH

			// Bilinear sample.
			r, g, b := p.sampleBilinear(u, v)

			idx := rowIdx + px*3
			p.buf[idx] = r
			p.buf[idx+1] = g
			p.buf[idx+2] = b
		}
	}

	if pos.Flipped {
		FlipVertical(p.buf, w, h)
	}
	DrawCrosshair(p.buf, w, h)
	DrawOSD(p.buf, w, h, pos, fps)
	return p.buf
}

// sampleBilinear performs bilinear interpolation on the source image at (u, v).
func (p *PanoRenderer) sampleBilinear(u, v float64) (uint8, uint8, uint8) {
	sw, sh := p.srcW, p.srcH
	swf, shf := float64(sw), float64(sh)

	// Wrap u horizontally, clamp v vertically.
	u = u - math.Floor(u/swf)*swf
	if v < 0 {
		v = 0
	} else if v >= shf-1 {
		v = shf - 1.001
	}

	x0 := int(u)
	y0 := int(v)
	fracX := u - float64(x0)
	fracY := v - float64(y0)

	x1 := (x0 + 1) % sw
	y1 := y0 + 1
	if y1 >= sh {
		y1 = sh - 1
	}

	r00, g00, b00 := p.srcPixel(x0, y0)
	r10, g10, b10 := p.srcPixel(x1, y0)
	r01, g01, b01 := p.srcPixel(x0, y1)
	r11, g11, b11 := p.srcPixel(x1, y1)

	r := bilerp(r00, r10, r01, r11, fracX, fracY)
	g := bilerp(g00, g10, g01, g11, fracX, fracY)
	b := bilerp(b00, b10, b01, b11, fracX, fracY)

	return r, g, b
}

func (p *PanoRenderer) srcPixel(x, y int) (float64, float64, float64) {
	idx := (y*p.srcW + x) * 3
	return float64(p.srcRGB[idx]), float64(p.srcRGB[idx+1]), float64(p.srcRGB[idx+2])
}

func bilerp(v00, v10, v01, v11, fx, fy float64) uint8 {
	top := v00*(1-fx) + v10*fx
	bot := v01*(1-fx) + v11*fx
	v := top*(1-fy) + bot*fy
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// loadImageRGB loads a JPEG or PNG image and returns its pixels as packed RGB24.
func loadImageRGB(path string) ([]byte, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	var img image.Image
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
	case ".png":
		img, err = png.Decode(f)
	default:
		return nil, 0, 0, fmt.Errorf("unsupported image format: %s", ext)
	}
	if err != nil {
		return nil, 0, 0, err
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	rgb := make([]byte, w*h*3)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			idx := (y*w + x) * 3
			rgb[idx] = uint8(r >> 8)
			rgb[idx+1] = uint8(g >> 8)
			rgb[idx+2] = uint8(b >> 8)
		}
	}

	return rgb, w, h, nil
}
