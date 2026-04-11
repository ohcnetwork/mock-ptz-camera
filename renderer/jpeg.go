// jpeg.go provides JPEG encoding utilities for converting RGB24 frame buffers
// to JPEG images, used by the MJPEG web preview stream.
//
// Two APIs are provided:
//   - EncodeJPEG: one-shot function that allocates fresh buffers each call.
//     Simple but causes GC pressure at high frame rates.
//   - JPEGEncoder: reusable encoder that pre-allocates an RGBA buffer and
//     reuses it across frames, eliminating per-frame allocation. The alpha
//     channel is pre-filled to 0xFF once at creation time.
//
// The render loop uses JPEGEncoder for the MJPEG stream (capped at ~15fps)
// to minimise allocation overhead.
package renderer

import (
	"bytes"
	"image"
	"image/jpeg"
)

// EncodeJPEG converts an RGB24 frame buffer to JPEG bytes.
// This is a one-shot convenience function that allocates fresh buffers.
// For high-frequency encoding, prefer JPEGEncoder which reuses buffers.
func EncodeJPEG(rgb []byte, width, height int) ([]byte, error) {
	pix := make([]byte, width*height*4)
	si, di := 0, 0
	for di < len(pix) {
		pix[di] = rgb[si]
		pix[di+1] = rgb[si+1]
		pix[di+2] = rgb[si+2]
		pix[di+3] = 255
		si += 3
		di += 4
	}
	img := &image.RGBA{
		Pix:    pix,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// JPEGEncoder reuses buffers across frames to avoid per-frame allocation.
type JPEGEncoder struct {
	width, height int
	rgba          []byte
	img           *image.RGBA
	buf           bytes.Buffer
	opts          *jpeg.Options
}

// NewJPEGEncoder creates a reusable JPEG encoder for the given frame size.
func NewJPEGEncoder(width, height int) *JPEGEncoder {
	rgba := make([]byte, width*height*4)
	// Pre-fill alpha channel once; Encode only updates RGB bytes.
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	return &JPEGEncoder{
		width:  width,
		height: height,
		rgba:   rgba,
		img: &image.RGBA{
			Pix:    rgba,
			Stride: width * 4,
			Rect:   image.Rect(0, 0, width, height),
		},
		opts: &jpeg.Options{Quality: 50},
	}
}

// Encode converts an RGB24 frame to JPEG, reusing internal buffers.
func (je *JPEGEncoder) Encode(rgb []byte) ([]byte, error) {
	dst := je.rgba
	si, di := 0, 0
	for di < len(dst) {
		dst[di] = rgb[si]
		dst[di+1] = rgb[si+1]
		dst[di+2] = rgb[si+2]
		// Alpha byte at dst[di+3] already 0xFF from init.
		si += 3
		di += 4
	}
	je.buf.Reset()
	if err := jpeg.Encode(&je.buf, je.img, je.opts); err != nil {
		return nil, err
	}
	out := make([]byte, je.buf.Len())
	copy(out, je.buf.Bytes())
	return out, nil
}
