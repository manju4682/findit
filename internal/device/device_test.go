package device

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
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

func TestFormatDeviceOpenError_ProvidesFullDiskAccessHint(t *testing.T) {
	msg := formatDeviceOpenError("/dev/rdisk4", errors.New("open /dev/rdisk4: operation not permitted"))
	for _, want := range []string{"Full Disk Access", "Privacy & Security", "/dev/rdisk4"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
