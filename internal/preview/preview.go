// Package preview turns a recovered file's bytes into a thumbnail. Images use
// the standard library; video uses an ffmpeg snapshot when available, macOS
// Quick Look otherwise, and falls back to a structural status.
package preview

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/safeimage"
	"github.com/manju4682/findit/internal/storage"
)

// DefaultMaxDim is the default longest-edge size of a generated thumbnail.
const DefaultMaxDim = 256

// readCap bounds how many bytes preview will pull for one file.
const readCap = 64 << 20

// Kind classifies what preview produced.
type Kind string

const (
	KindImage Kind = "image"
	KindVideo Kind = "video"
	KindNone  Kind = "none"
)

// Preview is a thumbnail plus a user-facing status for one file.
type Preview struct {
	Kind      Kind                   `json:"kind"`
	Thumbnail []byte                 `json:"-"` // PNG bytes; nil if unavailable
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	Status    model.AssessmentStatus `json:"status"`
	Note      string                 `json:"note,omitempty"`
}

// Generate produces a preview for f, reading its bytes from src. It never
// returns an error for an undecodable file — that is reported as a Partial or
// Uncertain status instead, so the UI can still list the file.
func Generate(ctx context.Context, src storage.Source, f model.RecoveredFile, maxDim int) *Preview {
	data, err := readFile(src, f)
	if err != nil {
		return &Preview{Kind: kindFor(ext(f)), Status: model.StatusPartial, Note: "could not read file bytes"}
	}
	return FromBytes(ctx, ext(f), data, maxDim)
}

// FromBytes produces a preview from a file's raw bytes and extension. This is
// the source-agnostic entrypoint: callers that already have the bytes (e.g. a
// filesystem file streamed via icat) use it directly.
func FromBytes(ctx context.Context, ext string, data []byte, maxDim int) *Preview {
	if maxDim <= 0 {
		maxDim = DefaultMaxDim
	}
	switch ext {
	case "jpg", "jpeg", "png", "gif":
		return imageFromBytes(data, maxDim)
	case "mp4", "mov", "m4v":
		return videoFromBytes(ctx, ext, data, maxDim)
	default:
		return &Preview{Kind: KindNone, Status: model.StatusUncertain}
	}
}

func kindFor(ext string) Kind {
	switch ext {
	case "jpg", "jpeg", "png", "gif":
		return KindImage
	case "mp4", "mov", "m4v":
		return KindVideo
	default:
		return KindNone
	}
}

func imageFromBytes(data []byte, maxDim int) *Preview {
	img, err := safeimage.Decode(bytes.NewReader(data))
	if errors.Is(err, safeimage.ErrTooLarge) {
		return &Preview{Kind: KindImage, Status: model.StatusUncertain, Note: "This image is too large to preview."}
	}
	if err != nil {
		return &Preview{Kind: KindImage, Status: model.StatusRawFragment,
			Note: "This looks like an image but is too incomplete to open — likely a fragment or partially overwritten."}
	}
	thumb := scaleDown(img, maxDim)
	var buf bytes.Buffer
	if err := png.Encode(&buf, thumb); err != nil {
		return &Preview{Kind: KindImage, Status: model.StatusUncertain, Note: "thumbnail encode failed"}
	}
	b := thumb.Bounds()
	return &Preview{
		Kind:      KindImage,
		Thumbnail: buf.Bytes(),
		Width:     b.Dx(),
		Height:    b.Dy(),
		Status:    model.StatusGood,
	}
}

func videoFromBytes(ctx context.Context, ext string, data []byte, maxDim int) *Preview {
	status := model.StatusUncertain
	if len(data) >= 8 && string(data[4:8]) == "ftyp" && bytes.Contains(data, []byte("moov")) {
		status = model.StatusGood // has the ftyp container and a moov index
	}
	p := &Preview{Kind: KindVideo, Status: status}

	thumb, err := videoSnapshot(ctx, ext, data, maxDim, status == model.StatusGood)
	if err != nil || len(thumb) == 0 {
		p.Note = "no snapshot available"
		return p
	}
	img, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		p.Note = "no snapshot available"
		return p
	}
	if b := img.Bounds(); b.Dx() > maxDim || b.Dy() > maxDim {
		var buf bytes.Buffer
		if png.Encode(&buf, scaleDown(img, maxDim)) == nil {
			thumb = buf.Bytes()
			img, _ = png.Decode(bytes.NewReader(thumb))
		}
	}
	p.Thumbnail = thumb
	b := img.Bounds()
	p.Width, p.Height = b.Dx(), b.Dy()
	return p
}

// snapshotTimeout bounds an external thumbnailer; Quick Look can stall on
// damaged video instead of failing.
var snapshotTimeout = 8 * time.Second

// findFFmpeg looks on PATH and in the Homebrew prefixes, since apps launched
// from Finder get a minimal PATH.
func findFFmpeg() (string, error) {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// videoSnapshot writes data to a private temp dir and asks ffmpeg (if
// installed) or, for structurally complete files, macOS Quick Look for one
// frame as PNG.
func videoSnapshot(ctx context.Context, ext string, data []byte, maxDim int, allowQuickLook bool) ([]byte, error) {
	ffmpeg, ffErr := findFFmpeg()
	if ffErr != nil && (runtime.GOOS != "darwin" || !allowQuickLook) {
		return nil, ffErr
	}
	dir, err := os.MkdirTemp("", "findit-preview-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in."+ext)
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()

	if ffErr == nil {
		out := filepath.Join(dir, "frame.png")
		scale := "scale='min(" + strconv.Itoa(maxDim) + ",iw)':-1"
		cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-loglevel", "error",
			"-i", in, "-frames:v", "1", "-vf", scale, "-y", out)
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		return os.ReadFile(out)
	}
	// Quick Look writes <dir>/<input name>.png.
	cmd := exec.CommandContext(ctx, "/usr/bin/qlmanage", "-t", "-s", strconv.Itoa(maxDim), "-o", dir, in)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(in + ".png")
}

// readFile reads a recovered file's bytes (up to readCap) by concatenating its
// extents through the read-only source.
func readFile(src storage.Source, f model.RecoveredFile) ([]byte, error) {
	var readers []io.Reader
	var total int64
	for _, r := range f.Extent {
		n := r.Length
		if total+n > readCap {
			n = readCap - total
		}
		readers = append(readers, io.NewSectionReader(src, r.Offset, n))
		if total += n; total >= readCap {
			break
		}
	}
	return io.ReadAll(io.MultiReader(readers...))
}

func ext(f model.RecoveredFile) string {
	if f.Ext != "" {
		return f.Ext
	}
	e := filepath.Ext(f.Name)
	if len(e) > 0 {
		return e[1:]
	}
	return ""
}

// scaleDown box-averages src to fit within maxDim on its longest edge. Box
// averaging gives good quality for downscaling using only the standard library.
func scaleDown(src image.Image, maxDim int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	dw, dh := sw, sh
	if sw > maxDim || sh > maxDim {
		if sw >= sh {
			dw = maxDim
			dh = max1(sh * maxDim / sw)
		} else {
			dh = maxDim
			dw = max1(sw * maxDim / sh)
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	avg := boxAverager(src)
	for dy := 0; dy < dh; dy++ {
		sy0 := b.Min.Y + dy*sh/dh
		sy1 := b.Min.Y + (dy+1)*sh/dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			sx0 := b.Min.X + dx*sw/dw
			sx1 := b.Min.X + (dx+1)*sw/dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			c := avg(sx0, sy0, sx1, sy1)
			i := dst.PixOffset(dx, dy)
			dst.Pix[i+0], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
	return dst
}

// boxAverager returns a function that averages src over [x0,x1)×[y0,y1).
// JPEG (YCbCr) and RGBA images are read straight from their pixel buffers,
// which is many times faster than the generic At() path for large photos.
func boxAverager(src image.Image) func(x0, y0, x1, y1 int) color.RGBA {
	switch im := src.(type) {
	case *image.YCbCr:
		return func(x0, y0, x1, y1 int) color.RGBA {
			var rs, gs, bs, n int
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					ci := im.COffset(x, y)
					r, g, b := color.YCbCrToRGB(im.Y[im.YOffset(x, y)], im.Cb[ci], im.Cr[ci])
					rs += int(r)
					gs += int(g)
					bs += int(b)
					n++
				}
			}
			return color.RGBA{uint8(rs / n), uint8(gs / n), uint8(bs / n), 0xFF}
		}
	case *image.RGBA:
		return func(x0, y0, x1, y1 int) color.RGBA {
			var r, g, b, a, n int
			for y := y0; y < y1; y++ {
				i := im.PixOffset(x0, y)
				for x := x0; x < x1; x++ {
					r += int(im.Pix[i])
					g += int(im.Pix[i+1])
					b += int(im.Pix[i+2])
					a += int(im.Pix[i+3])
					i += 4
					n++
				}
			}
			return color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)}
		}
	}
	return func(x0, y0, x1, y1 int) color.RGBA {
		var rs, gs, bs, as, n uint64
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				r, g, bl, a := src.At(x, y).RGBA()
				rs += uint64(r)
				gs += uint64(g)
				bs += uint64(bl)
				as += uint64(a)
				n++
			}
		}
		return color.RGBA{uint8((rs / n) >> 8), uint8((gs / n) >> 8), uint8((bs / n) >> 8), uint8((as / n) >> 8)}
	}
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
