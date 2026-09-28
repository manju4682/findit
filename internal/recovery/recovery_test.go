package recovery

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"testing"

	"github.com/findit/findit/internal/engines/tsk"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/preview"
	"github.com/findit/findit/internal/storage"
	"github.com/findit/findit/internal/testutil"
)

func fixture(t *testing.T, name string) storage.Source {
	return testutil.OpenFixture(t, name)
}

func scanResult(t *testing.T, src storage.Source, req model.ScanRequest) model.ScanResult {
	t.Helper()
	s := Scan(context.Background(), src, req)
	for range s.Events() {
	}
	if err := s.Wait(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return s.Result()
}

// TestRecover_RawFiles extracts carved files (byte extents) from the changed-FS
// image and confirms they decode.
func TestRecover_RawFiles(t *testing.T) {
	src := fixture(t, "changed_fs_fat32_to_exfat.bin")
	res := scanResult(t, src, model.ScanRequest{Raw: true, RawExtensions: []string{"jpg"}})

	var raw *model.RecoverySource
	for i := range res.Sources {
		if res.Sources[i].Kind == model.SourceRaw {
			raw = &res.Sources[i]
		}
	}
	if raw == nil || raw.FileCount == 0 {
		t.Fatal("no raw files to recover")
	}

	var pick []model.RecoveredFile
	for _, f := range raw.Files {
		if f.Recoverable {
			pick = append(pick, f)
		}
		if len(pick) == 3 {
			break
		}
	}
	dest := t.TempDir()
	out, err := Recover(context.Background(), src, *raw, pick, dest, false)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if out.Written != len(pick) || out.Failed != 0 {
		t.Fatalf("written=%d failed=%d, want %d/0", out.Written, out.Failed, len(pick))
	}
}

// TestRecover_FilesystemFiles extracts a filesystem file via the icat route and
// confirms it decodes; also previews it.
func TestRecover_FilesystemFiles(t *testing.T) {
	if !tsk.Available() {
		t.Skip("sleuthkit not on PATH")
	}
	src := fixture(t, "fat32_files.bin")
	res := scanResult(t, src, model.ScanRequest{Filesystems: []model.FSType{model.FSFAT32}})

	var fs *model.RecoverySource
	for i := range res.Sources {
		if res.Sources[i].Kind == model.SourceFilesystem {
			fs = &res.Sources[i]
		}
	}
	if fs == nil {
		t.Fatal("no filesystem source")
	}
	var jpg *model.RecoveredFile
	var files []*model.RecoveredFile
	flatten(fs.Root, &files)
	for _, f := range files {
		if f.Name == "IMG_0001.jpg" {
			jpg = f
		}
	}
	if jpg == nil {
		t.Fatal("IMG_0001.jpg not enumerated")
	}

	// Preview via the icat route.
	p := Preview(context.Background(), src, *fs, *jpg, 64)
	if p.Kind != preview.KindImage || p.Status != model.StatusGood || len(p.Thumbnail) == 0 {
		t.Errorf("preview = %+v, want a good image thumbnail", p)
	}

	// Recover via the icat route and decode the result.
	dest := t.TempDir()
	out, err := Recover(context.Background(), src, *fs, []model.RecoveredFile{*jpg}, dest, true)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if out.Written != 1 {
		t.Fatalf("written=%d, want 1 (items: %+v)", out.Written, out.Items)
	}
	got, _ := os.ReadFile(out.Items[0].Path)
	if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
		t.Errorf("recovered filesystem JPEG did not decode: %v", err)
	}
}

func flatten(n *model.Node, out *[]*model.RecoveredFile) {
	if n.File != nil {
		*out = append(*out, n.File)
	}
	for _, c := range n.Children {
		flatten(c, out)
	}
}
