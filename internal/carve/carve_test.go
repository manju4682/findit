package carve

import (
	"bytes"
	"context"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"github.com/findit/findit/internal/storage"
	"github.com/findit/findit/internal/testutil"
)

func fixture(t *testing.T) storage.Source {
	return testutil.OpenFixture(t, "changed_fs_fat32_to_exfat.bin")
}

func readExtent(t *testing.T, src storage.Source, c Carved) []byte {
	t.Helper()
	buf := make([]byte, c.Length)
	if _, err := src.ReadAt(buf, c.Offset); err != nil && err != io.EOF {
		t.Fatalf("ReadAt extent: %v", err)
	}
	return buf
}

func TestScan_CarvesValidMedia(t *testing.T) {
	src := fixture(t)
	carved, err := Scan(context.Background(), src, []string{"jpg", "png", "mp4"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	valid := map[string]int{}
	for _, c := range carved {
		if c.Valid {
			valid[c.Ext]++
		}
	}
	t.Logf("valid carved: jpg=%d png=%d mp4=%d (total findings=%d)",
		valid["jpg"], valid["png"], valid["mp4"], len(carved))

	if valid["jpg"] < 6 {
		t.Errorf("valid JPEGs = %d, want >= 6", valid["jpg"])
	}
	if valid["png"] < 3 {
		t.Errorf("valid PNGs = %d, want >= 3", valid["png"])
	}
	if valid["mp4"] < 3 {
		t.Errorf("valid MP4s = %d, want >= 3", valid["mp4"])
	}

	// Prove extent correctness by decoding carved media: if a measured byte
	// range decodes cleanly, its start+length are exactly right. At least one
	// valid file of each image kind must decode (some may be partially
	// overwritten, which is expected in real recovery).
	if !anyDecodes(src, carved, "jpg", func(b []byte) error { _, e := jpeg.Decode(bytes.NewReader(b)); return e }) {
		t.Error("no carved JPEG decoded cleanly")
	}
	if !anyDecodes(src, carved, "png", func(b []byte) error { _, e := png.Decode(bytes.NewReader(b)); return e }) {
		t.Error("no carved PNG decoded cleanly")
	}

	// MP4: confirm at least one measured range begins with an ftyp box.
	if c := firstValid(carved, "mp4"); c != nil {
		b := readExtent(t, src, *c)
		if len(b) < 8 || string(b[4:8]) != "ftyp" {
			t.Errorf("carved MP4 does not start with an ftyp box")
		}
	} else {
		t.Error("no valid MP4 found")
	}
}

// TestScan_OnlyRequestedExtensions verifies the raw scan is configurable: it
// carves only the extensions asked for, and ignores unknown ones.
func TestScan_OnlyRequestedExtensions(t *testing.T) {
	src := fixture(t)

	onlyPNG, err := Scan(context.Background(), src, []string{"png"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(onlyPNG) == 0 {
		t.Fatal("expected some PNG findings")
	}
	for _, c := range onlyPNG {
		if c.Ext != "png" {
			t.Errorf("got %q, want only png", c.Ext)
		}
	}

	if got, _ := Scan(context.Background(), src, []string{"heic"}); got != nil {
		t.Errorf("unknown ext should yield nil, got %d findings", len(got))
	}
	if got, _ := Scan(context.Background(), src, nil); got != nil {
		t.Errorf("empty selection should yield nil, got %d findings", len(got))
	}
}

func TestSupported(t *testing.T) {
	want := map[string]bool{"jpg": true, "png": true, "mp4": true, "pdf": true, "zip": true}
	for _, e := range Supported() {
		delete(want, e)
	}
	if len(want) != 0 {
		t.Errorf("Supported() missing: %v", want)
	}
}

// anyDecodes reports whether at least one valid carved file of the given
// extension passes the decode function.
func anyDecodes(src storage.Source, cs []Carved, ext string, decode func([]byte) error) bool {
	for _, c := range cs {
		if c.Ext != ext || !c.Valid {
			continue
		}
		buf := make([]byte, c.Length)
		if _, err := src.ReadAt(buf, c.Offset); err != nil && err != io.EOF {
			continue
		}
		if decode(buf) == nil {
			return true
		}
	}
	return false
}

func firstValid(cs []Carved, ext string) *Carved {
	for i := range cs {
		if cs[i].Ext == ext && cs[i].Valid {
			return &cs[i]
		}
	}
	return nil
}
