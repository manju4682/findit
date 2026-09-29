package preview

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/manju4682/findit/internal/carve"
	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/storage"
	"github.com/manju4682/findit/internal/testutil"
)

func fixture(t *testing.T) storage.Source {
	return testutil.OpenFixture(t, "changed_fs_fat32_to_exfat.bin")
}

func firstValidFile(t *testing.T, src storage.Source, ext string) model.RecoveredFile {
	t.Helper()
	carved, err := carve.Scan(context.Background(), src, []string{ext})
	if err != nil {
		t.Fatalf("carve.Scan: %v", err)
	}
	for _, c := range carved {
		if c.Valid {
			return model.RecoveredFile{
				ID: ext, Ext: c.Ext, Name: "f." + c.Ext,
				Size: c.Length, Extent: []model.ByteRange{{Offset: c.Offset, Length: c.Length}},
			}
		}
	}
	t.Fatalf("no valid %s in fixture", ext)
	return model.RecoveredFile{}
}

func TestGenerate_ImageThumbnails(t *testing.T) {
	src := fixture(t)
	for _, ext := range []string{"jpg", "png"} {
		f := firstValidFile(t, src, ext)
		p := Generate(context.Background(), src, f, 64)
		if p.Kind != KindImage {
			t.Errorf("%s: kind = %q, want image", ext, p.Kind)
		}
		if p.Status != model.StatusGood {
			t.Errorf("%s: status = %q, want Good", ext, p.Status)
		}
		if len(p.Thumbnail) == 0 {
			t.Fatalf("%s: no thumbnail produced", ext)
		}
		img, err := png.Decode(bytes.NewReader(p.Thumbnail))
		if err != nil {
			t.Fatalf("%s: thumbnail is not valid PNG: %v", ext, err)
		}
		b := img.Bounds()
		if b.Dx() > 64 || b.Dy() > 64 {
			t.Errorf("%s: thumbnail %dx%d exceeds max 64", ext, b.Dx(), b.Dy())
		}
		if p.Width != b.Dx() || p.Height != b.Dy() {
			t.Errorf("%s: reported %dx%d != actual %dx%d", ext, p.Width, p.Height, b.Dx(), b.Dy())
		}
	}
}

func TestGenerate_VideoDegradesGracefully(t *testing.T) {
	// The synthetic clip has no real frames; don't wait long for a thumbnailer.
	defer func(d time.Duration) { snapshotTimeout = d }(snapshotTimeout)
	snapshotTimeout = time.Second

	src := fixture(t)
	f := firstValidFile(t, src, "mp4")
	p := Generate(context.Background(), src, f, 64)
	if p.Kind != KindVideo {
		t.Errorf("kind = %q, want video", p.Kind)
	}
	// The synthetic MP4 has a valid ftyp box, so structure reads as Good even
	// without ffmpeg; the call must never error out.
	if p.Status == model.StatusPartial {
		t.Errorf("unexpected Partial status for a structurally valid container")
	}
}

// opaque hides an image's concrete type, forcing scaleDown's generic path.
type opaque struct{ image.Image }

// TestScaleDown_FastPathsMatchGeneric checks the direct-buffer averaging for
// JPEG (YCbCr) and RGBA images agrees with the generic At() path.
func TestScaleDown_FastPathsMatchGeneric(t *testing.T) {
	r := image.Rect(0, 0, 301, 199)
	ycc := image.NewYCbCr(r, image.YCbCrSubsampleRatio420)
	for i := range ycc.Y {
		ycc.Y[i] = uint8(i * 31)
	}
	for i := range ycc.Cb {
		ycc.Cb[i], ycc.Cr[i] = uint8(i*17), uint8(i*13)
	}
	rgba := image.NewRGBA(r)
	for i := range rgba.Pix {
		rgba.Pix[i] = uint8(i * 7)
	}
	for _, img := range []image.Image{ycc, rgba} {
		fast, slow := scaleDown(img, 64), scaleDown(opaque{img}, 64)
		if fast.Bounds() != slow.Bounds() {
			t.Fatalf("%T: bounds %v vs %v", img, fast.Bounds(), slow.Bounds())
		}
		for i := range fast.Pix {
			if d := int(fast.Pix[i]) - int(slow.Pix[i]); d < -2 || d > 2 {
				t.Fatalf("%T: pixel byte %d = %d, generic %d", img, i, fast.Pix[i], slow.Pix[i])
			}
		}
	}
}

func TestGenerate_UnknownType(t *testing.T) {
	src := fixture(t)
	p := Generate(context.Background(), src, model.RecoveredFile{Ext: "bin", Extent: []model.ByteRange{{Offset: 0, Length: 16}}}, 64)
	if p.Kind != KindNone {
		t.Errorf("kind = %q, want none", p.Kind)
	}
}
