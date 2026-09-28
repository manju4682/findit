package extract

import (
	"bytes"
	"context"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/findit/findit/internal/carve"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
	"github.com/findit/findit/internal/testutil"
)

func fixture(t *testing.T) storage.Source {
	return testutil.OpenFixture(t, "changed_fs_fat32_to_exfat.bin")
}

// carvedFiles builds RecoveredFiles from the first n valid carved files of ext,
// giving each a per-source directory path to exercise PreservePaths.
func carvedFiles(t *testing.T, src storage.Source, ext string, n int) []model.RecoveredFile {
	t.Helper()
	carved, err := carve.Scan(context.Background(), src, []string{ext})
	if err != nil {
		t.Fatalf("carve.Scan: %v", err)
	}
	var out []model.RecoveredFile
	for _, c := range carved {
		if !c.Valid {
			continue
		}
		i := len(out)
		out = append(out, model.RecoveredFile{
			ID:     ext + "-" + itoa(i),
			Ext:    c.Ext,
			Name:   "file_" + itoa(i) + "." + c.Ext,
			Path:   filepath.Join("DCIM", "sub"),
			Size:   c.Length,
			Extent: []model.ByteRange{{Offset: c.Offset, Length: c.Length}},
		})
		if len(out) == n {
			break
		}
	}
	if len(out) < n {
		t.Fatalf("wanted %d valid %s, got %d", n, ext, len(out))
	}
	return out
}

func TestExtract_WritesAndPreservesPaths(t *testing.T) {
	src := fixture(t)
	files := append(carvedFiles(t, src, "jpg", 3), carvedFiles(t, src, "png", 2)...)
	dest := t.TempDir()

	res, err := Extract(context.Background(), ExtentOpener(src), Request{
		Files: files, DestDir: dest, PreservePaths: true, ProtectSourcePath: src.Name(),
	}, nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Written != len(files) || res.Failed != 0 {
		t.Fatalf("written=%d failed=%d, want written=%d failed=0", res.Written, res.Failed, len(files))
	}

	for _, it := range res.Items {
		if it.Err != nil {
			t.Errorf("%s: %v", it.File.Name, it.Err)
			continue
		}
		// PreservePaths: file lands under DCIM/sub/<name>.
		wantDir := filepath.Join(dest, "DCIM", "sub")
		if filepath.Dir(it.Path) != wantDir {
			t.Errorf("path = %q, want dir %q", it.Path, wantDir)
		}
		fi, err := os.Stat(it.Path)
		if err != nil {
			t.Errorf("stat %q: %v", it.Path, err)
			continue
		}
		if fi.Size() != it.File.Size {
			t.Errorf("%s size = %d, want %d", it.File.Name, fi.Size(), it.File.Size)
		}
		b, _ := os.ReadFile(it.Path)
		switch it.File.Ext {
		case "jpg":
			if _, err := jpeg.Decode(bytes.NewReader(b)); err != nil {
				t.Errorf("extracted JPEG %q did not decode: %v", it.Path, err)
			}
		case "png":
			if _, err := png.Decode(bytes.NewReader(b)); err != nil {
				t.Errorf("extracted PNG %q did not decode: %v", it.Path, err)
			}
		}
	}
}

func TestExtract_RejectsSameDeviceDestination(t *testing.T) {
	src := fixture(t)
	dest := t.TempDir()
	_, err := Extract(context.Background(), ExtentOpener(src), Request{
		Files:             carvedFiles(t, src, "jpg", 1),
		DestDir:           dest,
		ProtectDevicePath: dest, // dest is trivially on the same device as itself
	}, nil)
	if err == nil {
		t.Fatal("expected same-device destination to be rejected")
	}
}

func TestExtract_SanitizesTraversalPaths(t *testing.T) {
	src := fixture(t)
	f := carvedFiles(t, src, "jpg", 1)[0]
	f.Path = filepath.Join("..", "..", "escape")
	f.Name = "evil.jpg"
	dest := t.TempDir()

	res, err := Extract(context.Background(), ExtentOpener(src), Request{
		Files: []model.RecoveredFile{f}, DestDir: dest, PreservePaths: true,
	}, nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("expected sanitized write to succeed, failed=%d", res.Failed)
	}
	got := res.Items[0].Path
	if rel, _ := filepath.Rel(dest, got); rel == "" || rel[0] == '.' {
		t.Errorf("written path %q escaped destination %q", got, dest)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
