// Package safeimage decodes untrusted images with a size guard. Recovered
// bytes can claim any dimensions in their header, and decoding a forged
// 60000×60000 JPEG would allocate gigabytes before failing.
package safeimage

import (
	"errors"
	"image"
	_ "image/gif" // register decoders for image.Decode
	_ "image/jpeg"
	_ "image/png"
	"io"
)

// MaxPixels is the largest image decoded (≈268 MP, above any consumer camera).
const MaxPixels = 1 << 28

// ErrTooLarge is returned when an image header exceeds MaxPixels.
var ErrTooLarge = errors.New("safeimage: image dimensions too large")

// Decode reads the header first and only decodes images within MaxPixels.
func Decode(r io.ReadSeeker) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, ErrTooLarge
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(r)
	return img, err
}
