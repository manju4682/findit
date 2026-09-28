package carve

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"image/jpeg"
	"image/png"
	"io"
	"os"
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

// TestScan_RejectsUndecodableJPEG proves the decode-verify step: a byte sequence
// that passes JPEG structural checks (SOI, a valid first marker, an SOS, and an
// EOI) but is not a real image must be reported as a fragment, not recoverable.
func TestScan_RejectsUndecodableJPEG(t *testing.T) {
	fake := []byte{0xFF, 0xD8, 0xFF, 0xE0} // SOI + APP0 marker
	fake = append(fake, bytes.Repeat([]byte{0x00}, 16)...)
	fake = append(fake, 0xFF, 0xDA) // SOS
	fake = append(fake, bytes.Repeat([]byte{0x11, 0x22, 0x33}, 64)...)
	fake = append(fake, 0xFF, 0xD9) // EOI

	src := memSource(t, fake)
	carved, err := Scan(context.Background(), src, []string{"jpg"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(carved) != 1 {
		t.Fatalf("got %d findings, want exactly 1", len(carved))
	}
	if carved[0].Valid {
		t.Error("undecodable JPEG marked Valid; decode-verify should reject it")
	}
}

// TestScan_VerifiesZIP proves the verify step generalizes beyond images: a real
// archive is recoverable, but one whose central directory is corrupt is a
// fragment even though its PK signatures survived.
func TestScan_VerifiesZIP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello findit")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	good := buf.Bytes()

	carved, err := Scan(context.Background(), memSource(t, good), []string{"zip"})
	if err != nil {
		t.Fatalf("Scan(good): %v", err)
	}
	if len(carved) != 1 || !carved[0].Valid {
		t.Fatalf("real zip should be one Valid finding, got %+v", carved)
	}

	// Break the central-directory file-header signature (PK\x01\x02) so the index
	// no longer parses, while the local-header and EOCD signatures still match.
	bad := append([]byte(nil), good...)
	cd := bytes.Index(bad, []byte{0x50, 0x4B, 0x01, 0x02})
	if cd < 0 {
		t.Fatal("test zip has no central-directory header")
	}
	bad[cd] = 0x00
	carved, err = Scan(context.Background(), memSource(t, bad), []string{"zip"})
	if err != nil {
		t.Fatalf("Scan(bad): %v", err)
	}
	if len(carved) != 1 {
		t.Fatalf("got %d findings, want exactly 1", len(carved))
	}
	if carved[0].Valid {
		t.Error("zip with a corrupt central directory marked Valid; verify should reject it")
	}
}

// mp4Box builds one ISO-BMFF box: 4-byte big-endian size, 4-byte type, payload.
func mp4Box(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload)))
	copy(out[4:8], typ)
	copy(out[8:], payload)
	return out
}

// TestScan_RequiresMoovForMP4 proves an MP4 is only recoverable when it carries a
// moov index box: ftyp+mdat alone (a common truncated/fragmented capture) is a
// fragment, while ftyp+moov+mdat verifies.
func TestScan_RequiresMoovForMP4(t *testing.T) {
	mdat := mp4Box("mdat", bytes.Repeat([]byte{0x42}, 4096))
	ftyp := mp4Box("ftyp", []byte("isom\x00\x00\x00\x00isommp41"))
	moov := mp4Box("moov", mp4Box("mvhd", make([]byte, 100)))

	noMoov, err := Scan(context.Background(), memSource(t, append(ftyp, mdat...)), []string{"mp4"})
	if err != nil {
		t.Fatalf("Scan(no moov): %v", err)
	}
	if len(noMoov) != 1 || noMoov[0].Valid {
		t.Fatalf("ftyp+mdat should be one fragment (Valid=false), got %+v", noMoov)
	}

	full := append(append(append([]byte(nil), ftyp...), moov...), mdat...)
	withMoov, err := Scan(context.Background(), memSource(t, full), []string{"mp4"})
	if err != nil {
		t.Fatalf("Scan(moov): %v", err)
	}
	if len(withMoov) != 1 || !withMoov[0].Valid {
		t.Fatalf("ftyp+moov+mdat should be one Valid finding, got %+v", withMoov)
	}
}

// memSource writes bytes to a temp image and opens it as a read-only Source.
func memSource(t *testing.T, b []byte) storage.Source {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "carve-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	src, err := storage.OpenImage(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
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
