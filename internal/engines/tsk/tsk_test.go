package tsk

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"
	"strings"
	"testing"

	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/testutil"
)

func fixturePath(t *testing.T) string {
	return testutil.FixturePath(t, "fat32_files.bin")
}

// TestArgs_ValidatesRequests guards the privileged helper: only fls/icat run,
// offsets can't be negative, and an inode can't smuggle in extra arguments.
func TestArgs_ValidatesRequests(t *testing.T) {
	tool, args, err := Args(Request{Op: "icat", Offset: 1 << 20, FSType: model.FSExFAT, Inode: "128-1"}, "/dev/disk4")
	if err != nil || tool != "icat" {
		t.Fatalf("valid icat rejected: tool=%q err=%v", tool, err)
	}
	want := []string{"-o", "2048", "-f", "exfat", "/dev/disk4", "128-1"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("icat args = %v, want %v", args, want)
	}

	for _, bad := range []Request{
		{Op: "rm"},
		{Op: "fls", Offset: -512},
		{Op: "icat", Inode: "5 -o 0"},
		{Op: "icat", Inode: "../etc/passwd"},
		{Op: "icat", Inode: ""},
	} {
		if _, _, err := Args(bad, "/dev/disk4"); err == nil {
			t.Errorf("Args(%+v) accepted, want rejection", bad)
		}
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

// fakeExec replays canned fls output, then returns err.
type fakeExec struct {
	out string
	err error
}

func (f fakeExec) Exec(_ context.Context, _ Request, w io.Writer) error {
	if _, err := io.WriteString(w, f.out); err != nil {
		return err
	}
	return f.err
}

const flsSample = "d/d 3:\tDCIM\t0\t0\t0\t0\t0\t0\t0\n" +
	"r/r 5:\tDCIM/IMG_1.jpg\t0\t0\t0\t0\t1234\t0\t0\n" +
	"r/r * 6:\tDCIM/100CANON/IMG_2.jpg\t0\t0\t0\t0\t99\t0\t0\n" +
	"d/d 3:\tDCIM\t0\t0\t0\t0\t0\t0\t0\n" +
	"r/r 7:\tnotes.txt\t0\t0\t0\t0\t5\t0\t0\n"

func TestEnumerate_BuildsTreeFromStream(t *testing.T) {
	eng := &Engine{exec: fakeExec{out: flsSample}, fsType: model.FSFAT32}
	src, err := eng.Enumerate(context.Background())
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if src.FileCount != 3 || src.RecoverableCount != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", src.FileCount, src.RecoverableCount)
	}
	if len(src.Root.Children) != 2 {
		t.Fatalf("root has %d children, want DCIM + notes.txt", len(src.Root.Children))
	}
	dcim := src.Root.Children[0]
	if dcim.Name != "DCIM" || len(dcim.Children) != 2 {
		t.Fatalf("DCIM = %q with %d children, want 2 (IMG_1.jpg, 100CANON)", dcim.Name, len(dcim.Children))
	}
	if sub := dcim.Children[1]; !sub.IsDir || sub.Name != "100CANON" || sub.Children[0].File.Path != "DCIM/100CANON" {
		t.Fatalf("nested directory not built correctly: %+v", sub)
	}
}

func TestEnumerate_ReportsToolFailure(t *testing.T) {
	eng := &Engine{exec: fakeExec{out: flsSample[:40], err: errors.New("fls: exit status 1: bad superblock")}}
	if _, err := eng.Enumerate(context.Background()); err == nil || !strings.Contains(err.Error(), "bad superblock") {
		t.Fatalf("err = %v, want the tool's error", err)
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
