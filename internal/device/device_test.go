package device

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAlignedReaderAt checks that arbitrary offset/length reads through the
// block-aligned wrapper return exactly the same bytes as the backing data.
func TestAlignedReaderAt(t *testing.T) {
	data := make([]byte, 2000)
	rand.New(rand.NewSource(7)).Read(data)
	a := newAlignedReaderAt(bytes.NewReader(data), 512, int64(len(data)))

	cases := []struct{ off, n int }{
		{0, 10}, {5, 500}, {512, 512}, {1000, 100}, {1900, 200}, {1990, 50},
	}
	for _, c := range cases {
		want := data[c.off:min(c.off+c.n, len(data))]
		got := make([]byte, c.n)
		n, _ := a.ReadAt(got, int64(c.off))
		if !bytes.Equal(got[:n], want) {
			t.Errorf("ReadAt(off=%d,n=%d) = %x, want %x", c.off, c.n, got[:n], want)
		}
	}

	// Reading at or past EOF yields io.EOF.
	if n, err := a.ReadAt(make([]byte, 8), int64(len(data))); n != 0 || err == nil {
		t.Errorf("ReadAt at EOF = (%d,%v), want (0, error)", n, err)
	}
}

func TestList_Runs(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("device enumeration only implemented on darwin")
	}
	devs, err := List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// At least the internal system disk should be present; validate shape.
	for _, d := range devs {
		if d.ID == "" || d.Node == "" || d.RawNode == "" {
			t.Errorf("device missing identifiers: %+v", d)
		}
		if d.Size <= 0 {
			t.Errorf("device %s has non-positive size %d", d.ID, d.Size)
		}
	}
	t.Logf("found %d device(s)", len(devs))
}

func TestFormatDeviceOpenError_ProvidesAdminHint(t *testing.T) {
	msg := formatDeviceOpenError("/dev/rdisk4", errors.New("open /dev/rdisk4: operation not permitted"))
	for _, want := range []string{"administrator privileges", "Full Disk Access", "safe copy", "/dev/rdisk4"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
}

// TestCheckImageDestination uses the volume holding the temp dir as the
// "source" drive to exercise the same-disk and free-space guards.
func TestCheckImageDestination(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("volume lookup only implemented on darwin")
	}
	dir := t.TempDir()
	v, err := VolumeOf(filepath.Join(dir, "not", "yet", "created"))
	if err != nil {
		t.Fatalf("VolumeOf: %v", err)
	}
	if v.Disk == "" || v.FSType == "" || v.Free <= 0 {
		t.Fatalf("incomplete volume info: %+v", v)
	}
	dest := filepath.Join(dir, "copy.bin")

	if err := CheckImageDestination("copy.bin", Device{ID: "disk999", Size: 1}); err == nil {
		t.Error("relative destination accepted")
	}
	if err := CheckImageDestination(dest, Device{ID: v.Disk, Size: 1}); err == nil {
		t.Error("destination on the source disk accepted")
	}
	if err := CheckImageDestination(dest, Device{ID: "disk999", Size: v.Free * 4}); err == nil || !strings.Contains(err.Error(), "free space") {
		t.Errorf("oversized image err = %v, want free-space error", err)
	}
	if err := CheckImageDestination(dest, Device{ID: "disk999", Size: 1 << 20}); err != nil {
		t.Errorf("valid destination rejected: %v", err)
	}
}
