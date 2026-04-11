package renderer

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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

	numWorkers int
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

	nw := runtime.NumCPU()
	if nw < 1 {
		nw = 1
	}

	return &PanoRenderer{
		width:      width,
		height:     height,
		buf:        make([]byte, width*height*3),
		srcRGB:     srcRGB,
		srcW:       srcW,
		srcH:       srcH,
		numWorkers: nw,
	}, nil
}

// Render produces an RGB24 frame showing the perspective projection into the
// panoramic sphere at the given PTZ position.
func (p *PanoRenderer) Render(pos ptz.Position, fps float64) []byte {
	w, h := p.width, p.height

	yaw := pos.Pan * math.Pi
	pitch := (pos.Tilt - ptz.TiltHorizon) * math.Pi / (2.0 * (ptz.TiltHorizon - ptz.TiltMin))
	fovH := (math.Pi / 2.0) / pos.ZoomX()

	sinY, cosY := math.Sincos(yaw)
	sinP, cosP := math.Sincos(pitch)

	halfW := float64(w) / 2.0
	halfH := float64(h) / 2.0
	focalLen := halfW / math.Tan(fovH/2.0)

	srcW := p.srcW
	srcH := p.srcH
	srcWf := float64(srcW)
	srcHf := float64(srcH)
	srcRGB := p.srcRGB
	srcStride := srcW * 3
	buf := p.buf

	invTwoPi := 0.5 / math.Pi
	invPi := 1.0 / math.Pi

	var wg sync.WaitGroup
	rowsPerWorker := (h + p.numWorkers - 1) / p.numWorkers

	for worker := 0; worker < p.numWorkers; worker++ {
		startRow := worker * rowsPerWorker
		endRow := startRow + rowsPerWorker
		if endRow > h {
			endRow = h
		}
		if startRow >= endRow {
			break
		}

		wg.Add(1)
		go func(startRow, endRow int) {
			defer wg.Done()
			for py := startRow; py < endRow; py++ {
				cy := halfH - float64(py) - 0.5
				rowIdx := py * w * 3

				for px := 0; px < w; px++ {
					cx := float64(px) - halfW + 0.5

					dx := cx
					dy := cy
					dz := focalLen

					invLen := 1.0 / math.Sqrt(dx*dx+dy*dy+dz*dz)
					dx *= invLen
					dy *= invLen
					dz *= invLen

					dy2 := dy*cosP + dz*sinP
					dz2 := -dy*sinP + dz*cosP

					dx3 := dx*cosY + dz2*sinY
					dz3 := -dx*sinY + dz2*cosY
					dy3 := dy2

					theta := fastAtan2(dx3, dz3)
					d := dy3
					if d < -1 {
						d = -1
					} else if d > 1 {
						d = 1
					}
					phi := fastAsin(d)

					u := (theta*invTwoPi + 0.5) * srcWf
					v := (0.5 - phi*invPi) * srcHf

					// Inline bilinear interpolation.
					u = u - math.Floor(u/srcWf)*srcWf
					if v < 0 {
						v = 0
					} else if v >= srcHf-1 {
						v = srcHf - 1.001
					}

					x0 := int(u)
					y0 := int(v)
					fracX := u - float64(x0)
					fracY := v - float64(y0)

					x1 := x0 + 1
					if x1 >= srcW {
						x1 = 0
					}
					y1 := y0 + 1
					if y1 >= srcH {
						y1 = srcH - 1
					}

					i00 := y0*srcStride + x0*3
					i10 := y0*srcStride + x1*3
					i01 := y1*srcStride + x0*3
					i11 := y1*srcStride + x1*3

					fx1 := 1 - fracX
					fy1 := 1 - fracY
					w00 := fx1 * fy1
					w10 := fracX * fy1
					w01 := fx1 * fracY
					w11 := fracX * fracY

					idx := rowIdx + px*3
					buf[idx] = uint8(float64(srcRGB[i00])*w00 + float64(srcRGB[i10])*w10 + float64(srcRGB[i01])*w01 + float64(srcRGB[i11])*w11)
					buf[idx+1] = uint8(float64(srcRGB[i00+1])*w00 + float64(srcRGB[i10+1])*w10 + float64(srcRGB[i01+1])*w01 + float64(srcRGB[i11+1])*w11)
					buf[idx+2] = uint8(float64(srcRGB[i00+2])*w00 + float64(srcRGB[i10+2])*w10 + float64(srcRGB[i01+2])*w01 + float64(srcRGB[i11+2])*w11)
				}
			}
		}(startRow, endRow)
	}
	wg.Wait()

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

// fastAtan2 is a polynomial approximation of math.Atan2, accurate to ~0.005 rad.
// Uses a minimax rational approximation to avoid the expensive libm atan2.
func fastAtan2(y, x float64) float64 {
	if x == 0 && y == 0 {
		return 0
	}
	ax := x
	if ax < 0 {
		ax = -ax
	}
	ay := y
	if ay < 0 {
		ay = -ay
	}

	// Ensure we compute atan of the smaller ratio for better accuracy.
	var a float64
	if ay < ax {
		a = ay / ax
	} else {
		a = ax / ay
	}

	// Polynomial minimax approximation for atan(a) on [0,1].
	s := a * a
	r := ((-0.0464964749*s+0.15931422)*s-0.327622764)*s*a + a

	if ay > ax {
		r = math.Pi/2 - r
	}
	if x < 0 {
		r = math.Pi - r
	}
	if y < 0 {
		r = -r
	}
	return r
}

// fastAsin is a polynomial approximation of math.Asin, accurate to ~0.0002 rad.
func fastAsin(x float64) float64 {
	neg := false
	if x < 0 {
		x = -x
		neg = true
	}
	if x > 1 {
		x = 1
	}
	// Handbook approximation (Abramowitz & Stegun 4.4.45).
	r := ((-0.0187293*x+0.0742610)*x-0.2121144)*x + 1.5707288
	r = math.Pi/2 - math.Sqrt(1.0-x)*r
	if neg {
		return -r
	}
	return r
}
