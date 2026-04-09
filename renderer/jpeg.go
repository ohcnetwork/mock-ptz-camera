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
