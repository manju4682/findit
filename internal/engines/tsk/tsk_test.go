package tsk

import (
	"bytes"
	"context"
	"image/jpeg"
	"testing"

	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/testutil"
)

func fixturePath(t *testing.T) string {
	return testutil.FixturePath(t, "fat32_files.bin")
}

func flatten(n *model.Node, out *[]*model.RecoveredFile) {
	if n.File != nil {
		*out = append(*out, n.File)
	}
	for _, c := range n.Children {
		flatten(c, out)
	}
}

func TestEnumerate_FAT32(t *testing.T) {
	if !Available() {
		t.Skip("sleuthkit (fls/icat) not on PATH")
	}
	eng := New(fixturePath(t), 0, model.FSFAT32)
	src, err := eng.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if src.Kind != model.SourceFilesystem || src.FSType != model.FSFAT32 {
		t.Fatalf("source = %+v, want filesystem/FAT32", src)
	}

	var files []*model.RecoveredFile
	flatten(src.Root, &files)
	byName := map[string]*model.RecoveredFile{}
	for _, f := range files {
		byName[f.Name] = f
	}

	// Present file with a real directory path and a non-zero size.
	present := byName["IMG_0001.jpg"]
	if present == nil {
		t.Fatalf("IMG_0001.jpg not enumerated; got %v", names(files))
	}
	if present.Path != "DCIM" {
		t.Errorf("IMG_0001.jpg path = %q, want DCIM", present.Path)
	}
	if present.Size == 0 {
		t.Errorf("IMG_0001.jpg size = 0, want > 0")
	}
	if present.Deleted || !present.Recoverable {
		t.Errorf("present file flagged deleted/unrecoverable: %+v", present)
	}

	// Deleted file: TSK renames the first char (_MG_0004.jpg) and flags it.
	var deleted *model.RecoveredFile
	for _, f := range files {
		if f.Deleted {
			deleted = f
			break
		}
	}
	if deleted == nil {
		t.Errorf("expected at least one deleted file")
	} else if deleted.Recoverable {
		t.Errorf("deleted file should not be marked Recoverable: %+v", deleted)
	}
}

func TestExtractFile_DecodesPresentImage(t *testing.T) {
	if !Available() {
		t.Skip("sleuthkit (fls/icat) not on PATH")
	}
	eng := New(fixturePath(t), 0, model.FSFAT32)
	src, err := eng.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	var files []*model.RecoveredFile
	flatten(src.Root, &files)

	var jpg *model.RecoveredFile
	for _, f := range files {
		if f.Name == "IMG_0001.jpg" {
			jpg = f
			break
		}
	}
	if jpg == nil {
		t.Fatal("IMG_0001.jpg not found")
	}

	var buf bytes.Buffer
	if err := eng.ExtractFile(context.Background(), jpg.Ref, &buf); err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if int64(buf.Len()) != jpg.Size {
		t.Errorf("extracted %d bytes, want %d", buf.Len(), jpg.Size)
	}
	if _, err := jpeg.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("extracted file is not a valid JPEG: %v", err)
	}
}

func names(files []*model.RecoveredFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}
