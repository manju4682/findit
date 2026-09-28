// Command genmedia writes deterministic sample media (JPEG/PNG/MP4) into a
// directory. It exists only to populate synthetic recovery fixtures — the
// output files have distinct, reproducible content so a recovery run can be
// verified against them.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", ".", "output directory")
	prefix := flag.String("prefix", "FILE", "filename prefix")
	nJPG := flag.Int("jpg", 0, "number of JPEG files")
	nPNG := flag.Int("png", 0, "number of PNG files")
	nMP4 := flag.Int("mp4", 0, "number of MP4 files")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}

	for i := 1; i <= *nJPG; i++ {
		p := filepath.Join(*out, fmt.Sprintf("%s_%04d.jpg", *prefix, i))
		if err := writeImage(p, i, "jpg"); err != nil {
			fail(err)
		}
	}
	for i := 1; i <= *nPNG; i++ {
		p := filepath.Join(*out, fmt.Sprintf("%s_%04d.png", *prefix, i))
		if err := writeImage(p, i, "png"); err != nil {
			fail(err)
		}
	}
	for i := 1; i <= *nMP4; i++ {
		p := filepath.Join(*out, fmt.Sprintf("%s_%04d.mp4", *prefix, i))
		if err := writeMP4(p, i); err != nil {
			fail(err)
		}
	}
}

// writeImage draws a distinct pattern (index-derived colours, a diagonal band
// and a filled block) so the encoded bytes are non-trivial and unique per file.
func writeImage(path string, seed int, format string) error {
	const w, h = 320, 240
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := color.RGBA{R: uint8(37*seed) % 255, G: uint8(97*seed) % 255, B: uint8(151*seed) % 255, A: 255}
	band := color.RGBA{R: 255 - base.R, G: 255 - base.G, B: 255 - base.B, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := base
			if (x+y+seed)%64 < 8 { // diagonal band
				c = band
			}
			if x > w/4 && x < w*3/4 && y > h/3 && y < h*2/3 { // centre block
				c = color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: uint8((x + y + seed) % 255), A: 255}
			}
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if format == "png" {
		return png.Encode(f, img)
	}
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
}

// writeMP4 emits a minimal but signature-valid MP4: an `ftyp` box, a `moov`
// index box (a nested `mvhd`), then a deterministic `mdat` payload. The moov box
// is what marks a carved MP4 as playable rather than an unrecoverable fragment.
func writeMP4(path string, seed int) error {
	var buf []byte
	buf = append(buf, box("ftyp", append([]byte("isom\x00\x00\x00\x00"), []byte("isommp41")...))...)
	buf = append(buf, box("moov", box("mvhd", make([]byte, 100)))...)

	payload := make([]byte, 8192)
	for i := range payload {
		payload[i] = byte((i*7 + seed*13) % 251)
	}
	buf = append(buf, box("mdat", payload)...)
	return os.WriteFile(path, buf, 0o644)
}

// box builds an ISO-BMFF box: 4-byte big-endian size, 4-byte type, payload.
func box(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(size))
	copy(out[4:8], typ)
	copy(out[8:], payload)
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genmedia:", err)
	os.Exit(1)
}
