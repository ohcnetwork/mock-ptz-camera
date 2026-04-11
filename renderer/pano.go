// pano.go implements the core panoramic renderer — the CPU-intensive hot path.
//
// It takes an equirectangular 360° panoramic source image and projects a
// virtual perspective camera into it based on the current PTZ (Pan/Tilt/Zoom)
// state. This simulates a real PTZ camera navigating a spherical scene.
//
// Rendering pipeline per frame:
//  1. Convert PTZ state → yaw/pitch/focal-length.
//  2. Precompute per-column yaw rotation products.
//  3. Dispatch row ranges to a persistent worker pool (NumCPU goroutines).
//  4. Each worker: for each pixel, compute 3D ray direction, convert to
//     spherical coordinates (θ, φ), map to equirectangular UV, and sample
//     the source image using fixed-point 8.8 bilinear interpolation.
//  5. Cache the projected frame; if PTZ hasn't changed, only redraw the
//     OSD region (~50KB) instead of re-rendering the full frame (~6MB).
//  6. Draw OSD overlay (timestamp, FPS, PTZ info).
//
// Performance optimisations:
//   - Precomputed per-column arrays (colCx, colCxSq) avoid redundant math.
//   - Per-frame column products (colCxCosY, colCxSinY) precomputed once.
//   - Fixed-point 8.8 bilinear interpolation (integer multiply + shift)
//     instead of floating-point.
//   - fastAtan2 / fastAtanPos polynomial approximations (~0.005 rad accuracy)
//     replace expensive libm atan2 calls.
//   - Persistent worker pool avoids goroutine spawn overhead per frame.
//   - OSD save/restore avoids full re-render when only the OSD changes.
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

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// PanoRenderer renders a virtual perspective camera view from an equirectangular
// 360° panoramic source image. It is the primary renderer used in production
// (as opposed to TestRenderer which generates synthetic patterns).
type PanoRenderer struct {
	width, height int    // output frame dimensions in pixels
	buf           []byte // single frame buffer (width*height*3 RGB24)
	osdSave       []byte // saved clean pixels under the OSD region for cache restore

	// Source equirectangular image stored as RGB24.
	srcRGB     []byte
	srcW, srcH int

	numWorkers int          // number of parallel render workers (= runtime.NumCPU)
	workCh     []chan [2]int // per-worker channel carrying [startRow, endRow) ranges
	doneCh     chan struct{} // workers send here when their row range is complete

	// Precomputed per-column values (constant for the lifetime of the renderer).
	colCx   []float64 // colCx[px] = (px - width/2 + 0.5), pixel offset from centre
	colCxSq []float64 // colCx[px]², used in hypot calculation

	// Per-frame scratch arrays (allocated once at init, overwritten each Render call).
	colCxCosY []float64 // colCx[px] * cos(yaw) — precomputed per-column yaw rotation
	colCxSinY []float64 // colCx[px] * sin(yaw) — precomputed per-column yaw rotation

	// Cached PTZ position: skip full re-projection when PTZ hasn't moved.
	cachedPos ptz.Position
	hasCache  bool

	// OSD dirty-region bounds (precomputed at init). On cache hit, only this
	// small rectangle needs to be restored and redrawn.
	osdW, osdH int

	// rp holds the per-frame projection parameters shared with worker goroutines.
	// Set before dispatching workers in Render(); workers read it concurrently.
	rp renderParams
}

// NewPanoRenderer loads an equirectangular panoramic image from disk and
// initialises a renderer that can project virtual perspective views into it.
//
// The image is decoded to RGB24 and stored in memory. A persistent pool of
// NumCPU worker goroutines is started for parallel row rendering.
// Per-column projection constants are precomputed once.
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

	halfW := float64(width) / 2.0
	colCx := make([]float64, width)
	colCxSq := make([]float64, width)
	for px := 0; px < width; px++ {
		cx := float64(px) - halfW + 0.5
		colCx[px] = cx
		colCxSq[px] = cx * cx
	}

	p := &PanoRenderer{
		width:      width,
		height:     height,
		buf:        make([]byte, width*height*3),
		srcRGB:     srcRGB,
		srcW:       srcW,
		srcH:       srcH,
		numWorkers: nw,
		colCx:      colCx,
		colCxSq:    colCxSq,
		colCxCosY:  make([]float64, width),
		colCxSinY:  make([]float64, width),
	}

	// Precompute OSD dirty-region bounds (matches DrawOSD layout + shadow offset).
	const osdScale = 2
	const osdPad = 8
	const osdMaxChars = 25 // generous upper bound for longest OSD line
	const osdLines = 3
	p.osdW = osdMaxChars*(fontW+1)*osdScale + osdPad*2 + 2 // +2 for shadow
	p.osdH = osdLines*((fontH+2)*osdScale) + osdPad*2 + 2
	if p.osdW > width {
		p.osdW = width
	}
	if p.osdH > height {
		p.osdH = height
	}
	p.osdSave = make([]byte, p.osdW*p.osdH*3)

	// Start persistent worker pool — avoids goroutine spawn overhead per frame.
	p.workCh = make([]chan [2]int, nw)
	p.doneCh = make(chan struct{}, nw)
	for i := 0; i < nw; i++ {
		p.workCh[i] = make(chan [2]int, 1)
		go p.worker(p.workCh[i])
	}

	return p, nil
}

// renderParams holds the per-frame projection parameters shared across workers.
// These are computed once per frame in Render() and read concurrently by
// all worker goroutines. The struct is value-copied into PanoRenderer.rp.
type renderParams struct {
	w, h                             int     // output dimensions
	sinY, cosY, sinP, cosP, focalLen float64 // trigonometric values for yaw/pitch + focal length
	halfH                            float64 // h/2.0 (vertical centre offset)
	srcW, srcH                       int     // source image dimensions
	srcWf, srcHf                     float64 // source dimensions as float64 (avoid per-pixel conversion)
	srcRGB                           []byte  // source image pixel data (RGB24)
	srcStride                        int     // source row stride in bytes (srcW * 3)
	invTwoPi, invPi, srcVMax         float64 // precomputed constants for spherical→UV mapping
	colCxSq, cxCosY, cxSinY         []float64 // per-column precomputed arrays
	buf                              []byte    // destination frame buffer
}

// worker is a persistent goroutine that processes row ranges dispatched via ch.
// It blocks on the channel until a [startRow, endRow) range is sent, renders
// those rows, and signals completion on doneCh. The goroutine lives for the
// lifetime of the renderer.
func (p *PanoRenderer) worker(ch chan [2]int) {
	for rng := range ch {
		p.renderRows(rng[0], rng[1])
		p.doneCh <- struct{}{}
	}
}

// renderRows renders pixel rows [startRow, endRow) of the current frame.
//
// For each pixel (px, py):
//  1. Compute the 3D ray direction from the virtual camera through this pixel,
//     applying pitch rotation (around X-axis) and yaw rotation (around Y-axis).
//  2. Convert the 3D direction to spherical coordinates:
//     - θ (theta) = atan2(dx, dz) → horizontal angle (longitude)
//     - φ (phi)   = atan(dy / hypot(dx,dz)) → vertical angle (latitude)
//  3. Map spherical coords to equirectangular UV:
//     - u = (θ/2π + 0.5) × srcWidth
//     - v = (0.5 - φ/π) × srcHeight
//  4. Sample the source image at (u, v) using fixed-point 8.8 bilinear
//     interpolation (256 = 1.0) for sub-pixel accuracy without floats.
func (p *PanoRenderer) renderRows(startRow, endRow int) {
	rp := &p.rp
	w := rp.w
	w3 := w * 3
	for py := startRow; py < endRow; py++ {
		cy := rp.halfH - float64(py) - 0.5
		rowIdx := py * w3

		dy2 := cy*rp.cosP + rp.focalLen*rp.sinP
		dz2 := -cy*rp.sinP + rp.focalLen*rp.cosP

		dz2sinY := dz2 * rp.sinY
		dz2cosY := dz2 * rp.cosY
		dz2Sq := dz2 * dz2

		idx := rowIdx
		for px := 0; px < w; px++ {
			dx3 := rp.cxCosY[px] + dz2sinY
			dz3 := dz2cosY - rp.cxSinY[px]

			theta := fastAtan2(dx3, dz3)

			hypot := math.Sqrt(rp.colCxSq[px] + dz2Sq)
			phi := fastAtanPos(dy2, hypot)

			u := (theta*rp.invTwoPi + 0.5) * rp.srcWf
			v := (0.5 - phi*rp.invPi) * rp.srcHf

			if u < 0 {
				u += rp.srcWf
			} else if u >= rp.srcWf {
				u -= rp.srcWf
			}
			if v < 0 {
				v = 0
			} else if v > rp.srcVMax {
				v = rp.srcVMax
			}

			x0 := int(u)
			y0 := int(v)

			fx := uint32((u - float64(x0)) * 256)
			fy := uint32((v - float64(y0)) * 256)
			ifx := 256 - fx
			ify := 256 - fy
			w00 := ifx * ify
			w10 := fx * ify
			w01 := ifx * fy
			w11 := fx * fy

			x1 := x0 + 1
			if x1 >= rp.srcW {
				x1 = 0
			}
			y1 := y0 + 1
			if y1 >= rp.srcH {
				y1 = rp.srcH - 1
			}

			row0 := y0 * rp.srcStride
			row1 := y1 * rp.srcStride
			x03 := x0 * 3
			x13 := x1 * 3
			i00 := row0 + x03
			i10 := row0 + x13
			i01 := row1 + x03
			i11 := row1 + x13

			src := rp.srcRGB
			rp.buf[idx] = byte((uint32(src[i00])*w00 + uint32(src[i10])*w10 + uint32(src[i01])*w01 + uint32(src[i11])*w11) >> 16)
			rp.buf[idx+1] = byte((uint32(src[i00+1])*w00 + uint32(src[i10+1])*w10 + uint32(src[i01+1])*w01 + uint32(src[i11+1])*w11) >> 16)
			rp.buf[idx+2] = byte((uint32(src[i00+2])*w00 + uint32(src[i10+2])*w10 + uint32(src[i01+2])*w01 + uint32(src[i11+2])*w11) >> 16)
			idx += 3
		}
	}
}

// saveOSDRegion copies the clean pixels under the OSD area into osdSave.
func (p *PanoRenderer) saveOSDRegion() {
	stride := p.width * 3
	osdBytes := p.osdW * 3
	di := 0
	for y := 0; y < p.osdH; y++ {
		off := y * stride
		copy(p.osdSave[di:di+osdBytes], p.buf[off:off+osdBytes])
		di += osdBytes
	}
}

// restoreOSDRegion restores clean pixels under the OSD area from osdSave.
func (p *PanoRenderer) restoreOSDRegion() {
	stride := p.width * 3
	osdBytes := p.osdW * 3
	si := 0
	for y := 0; y < p.osdH; y++ {
		off := y * stride
		copy(p.buf[off:off+osdBytes], p.osdSave[si:si+osdBytes])
		si += osdBytes
	}
}

// Render produces an RGB24 frame showing the perspective projection of the
// panoramic source image at the given PTZ position, with OSD overlay.
//
// Caching: if the PTZ position hasn't changed since the last call, only the
// OSD region is restored from a saved copy and redrawn — the expensive
// full-frame projection is skipped entirely. This makes static PTZ positions
// essentially free.
func (p *PanoRenderer) Render(pos ptz.Position, fps float64) []byte {
	w, h := p.width, p.height

	// If PTZ position hasn't changed, reuse cached pano projection.
	// Only restore the small OSD region (~50KB) from saved clean pixels.
	if p.hasCache && pos.Pan == p.cachedPos.Pan && pos.Tilt == p.cachedPos.Tilt && pos.Zoom == p.cachedPos.Zoom {
		p.restoreOSDRegion()
		DrawOSD(p.buf, w, h, pos, fps)
		return p.buf
	}

	yaw := pos.Pan * math.Pi
	pitch := (pos.Tilt - ptz.TiltHorizon) * math.Pi / (2.0 * (ptz.TiltHorizon - ptz.TiltMin))
	fovH := (math.Pi / 2.0) / pos.ZoomX()

	sinY, cosY := math.Sincos(yaw)
	sinP, cosP := math.Sincos(pitch)
	focalLen := (float64(w) / 2.0) / math.Tan(fovH/2.0)

	// Precompute per-column yaw products.
	for px := 0; px < w; px++ {
		p.colCxCosY[px] = p.colCx[px] * cosY
		p.colCxSinY[px] = p.colCx[px] * sinY
	}

	// Set shared render params for worker goroutines.
	p.rp = renderParams{
		w: w, h: h,
		sinY: sinY, cosY: cosY, sinP: sinP, cosP: cosP,
		focalLen: focalLen,
		halfH:    float64(h) / 2.0,
		srcW: p.srcW, srcH: p.srcH,
		srcWf: float64(p.srcW), srcHf: float64(p.srcH),
		srcRGB:    p.srcRGB,
		srcStride: p.srcW * 3,
		invTwoPi:  0.5 / math.Pi,
		invPi:     1.0 / math.Pi,
		srcVMax:   float64(p.srcH) - 1.001,
		colCxSq:   p.colCxSq,
		cxCosY:    p.colCxCosY,
		cxSinY:    p.colCxSinY,
		buf:       p.buf,
	}

	// Dispatch row ranges to persistent workers.
	rowsPerWorker := (h + p.numWorkers - 1) / p.numWorkers
	active := 0
	for i := 0; i < p.numWorkers; i++ {
		startRow := i * rowsPerWorker
		endRow := startRow + rowsPerWorker
		if endRow > h {
			endRow = h
		}
		if startRow >= endRow {
			break
		}
		p.workCh[i] <- [2]int{startRow, endRow}
		active++
	}
	for i := 0; i < active; i++ {
		<-p.doneCh
	}

	p.cachedPos = pos
	p.hasCache = true

	// Save the clean OSD region before drawing OSD text.
	p.saveOSDRegion()
	DrawOSD(p.buf, w, h, pos, fps)
	return p.buf
}

// loadImageRGB loads a JPEG or PNG image file and returns its pixel data as
// a packed RGB24 byte slice along with the image dimensions. The image format
// is determined by file extension.
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

// fastAtanPos computes atan2(y, x) for the case where x >= 0.
// Result is in [-π/2, π/2]. This specialised version eliminates the x<0
// octant branch of fastAtan2 and is used for computing the elevation angle
// (phi) where the denominator (hypot) is always non-negative.
//
// Uses the same polynomial minimax approximation as fastAtan2.
func fastAtanPos(y, x float64) float64 {
	if x == 0 {
		if y > 0 {
			return math.Pi / 2
		}
		if y < 0 {
			return -math.Pi / 2
		}
		return 0
	}
	ay := y
	if ay < 0 {
		ay = -ay
	}

	var a float64
	if ay < x {
		a = ay / x
	} else {
		a = x / ay
	}

	s := a * a
	r := ((-0.0464964749*s+0.15931422)*s-0.327622764)*s*a + a

	if ay > x {
		r = math.Pi/2 - r
	}
	if y < 0 {
		r = -r
	}
	return r
}

// fastAtan2 is a polynomial approximation of math.Atan2, accurate to ~0.005 rad.
//
// Uses a minimax rational approximation on [0, 1] and maps all quadrants
// back via sign and complement operations. This avoids the expensive libm
// atan2 which is a significant cost in the per-pixel inner loop.
//
// The approximation polynomial is:
//   atan(a) ≈ ((-0.0464964749·a² + 0.15931422)·a² - 0.327622764)·a² · a + a
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
// Uses the Abramowitz & Stegun (4.4.45) handbook approximation.
// Input is clamped to [-1, 1].
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
