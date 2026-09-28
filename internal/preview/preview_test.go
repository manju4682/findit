package preview

import (
	"bytes"
	"context"
	"image/png"
	"testing"

	"github.com/findit/findit/internal/carve"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
	"github.com/findit/findit/internal/testutil"
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

func TestGenerate_UnknownType(t *testing.T) {
	src := fixture(t)
	p := Generate(context.Background(), src, model.RecoveredFile{Ext: "bin", Extent: []model.ByteRange{{Offset: 0, Length: 16}}}, 64)
	if p.Kind != KindNone {
		t.Errorf("kind = %q, want none", p.Kind)
	}
}
