package renderer

import (
	"bytes"
	"image"
	"image/jpeg"
)

// EncodeJPEG converts an RGB24 frame buffer to JPEG bytes.
func EncodeJPEG(rgb []byte, width, height int) ([]byte, error) {
	img := &image.NRGBA{
		Pix:    rgbToNRGBA(rgb, width, height),
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
	nrgba         []byte
	img           *image.NRGBA
	buf           bytes.Buffer
	opts          *jpeg.Options
}

// NewJPEGEncoder creates a reusable JPEG encoder for the given frame size.
func NewJPEGEncoder(width, height int) *JPEGEncoder {
	nrgba := make([]byte, width*height*4)
	return &JPEGEncoder{
		width:  width,
		height: height,
		nrgba:  nrgba,
		img: &image.NRGBA{
			Pix:    nrgba,
			Stride: width * 4,
			Rect:   image.Rect(0, 0, width, height),
		},
		opts: &jpeg.Options{Quality: 75},
	}
}

// Encode converts an RGB24 frame to JPEG, reusing internal buffers.
func (je *JPEGEncoder) Encode(rgb []byte) ([]byte, error) {
	n := je.width * je.height
	for i := 0; i < n; i++ {
		je.nrgba[i*4] = rgb[i*3]
		je.nrgba[i*4+1] = rgb[i*3+1]
		je.nrgba[i*4+2] = rgb[i*3+2]
		je.nrgba[i*4+3] = 255
	}
	je.buf.Reset()
	if err := jpeg.Encode(&je.buf, je.img, je.opts); err != nil {
		return nil, err
	}
	out := make([]byte, je.buf.Len())
	copy(out, je.buf.Bytes())
	return out, nil
}

// rgbToNRGBA converts packed RGB24 bytes to NRGBA pixel data.
func rgbToNRGBA(rgb []byte, w, h int) []byte {
	nrgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		nrgba[i*4+0] = rgb[i*3+0]
		nrgba[i*4+1] = rgb[i*3+1]
		nrgba[i*4+2] = rgb[i*3+2]
		nrgba[i*4+3] = 255
	}
	return nrgba
}
