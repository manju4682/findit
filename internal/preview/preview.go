// Package preview turns a recovered file's bytes into a thumbnail. Images use
// the standard library; video uses an ffmpeg snapshot when available and
// otherwise falls back to a structural status.
package preview

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.Decode
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
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
	case "jpg", "jpeg", "png":
		return imageFromBytes(data, maxDim)
	case "mp4", "mov", "m4v":
		return videoFromBytes(ctx, ext, data, maxDim)
	default:
		return &Preview{Kind: KindNone, Status: model.StatusUncertain}
	}
}

func kindFor(ext string) Kind {
	switch ext {
	case "jpg", "jpeg", "png":
		return KindImage
	case "mp4", "mov", "m4v":
		return KindVideo
	default:
		return KindNone
	}
}

func imageFromBytes(data []byte, maxDim int) *Preview {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return &Preview{Kind: KindImage, Status: model.StatusPartial, Note: "image did not decode fully"}
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
	if len(data) >= 8 && string(data[4:8]) == "ftyp" {
		status = model.StatusGood // container structure looks valid
	}
	p := &Preview{Kind: KindVideo, Status: status}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		p.Note = "install ffmpeg for video snapshots"
		return p
	}
	thumb, err := ffmpegSnapshot(ctx, ext, data, maxDim)
	if err != nil || len(thumb) == 0 {
		p.Note = "no snapshot available"
		return p
	}
	p.Thumbnail = thumb
	if img, err := png.Decode(bytes.NewReader(thumb)); err == nil {
		b := img.Bounds()
		p.Width, p.Height = b.Dx(), b.Dy()
	}
	return p
}

// ffmpegSnapshot writes data to a temp path and asks ffmpeg for one frame,
// scaled to fit maxDim, encoded as PNG.
func ffmpegSnapshot(ctx context.Context, ext string, data []byte, maxDim int) ([]byte, error) {
	dir, err := os.MkdirTemp("", "findit-preview-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in."+ext)
	out := filepath.Join(dir, "frame.png")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}

	scale := "scale='min(" + strconv.Itoa(maxDim) + ",iw)':-1"
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error",
		"-i", in, "-frames:v", "1", "-vf", scale, "-y", out)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
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
			var rs, gs, bs, as, count uint64
			for y := sy0; y < sy1; y++ {
				for x := sx0; x < sx1; x++ {
					r, g, bl, a := src.At(x, y).RGBA()
					rs += uint64(r)
					gs += uint64(g)
					bs += uint64(bl)
					as += uint64(a)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i+0] = uint8((rs / count) >> 8)
			dst.Pix[i+1] = uint8((gs / count) >> 8)
			dst.Pix[i+2] = uint8((bs / count) >> 8)
			dst.Pix[i+3] = uint8((as / count) >> 8)
		}
	}
	return dst
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
