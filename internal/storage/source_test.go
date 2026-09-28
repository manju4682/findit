package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img.bin")
	want := []byte("HELLO-FINDIT-0123456789")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := OpenImage(path)
	if err != nil {
		t.Fatalf("OpenImage: %v", err)
	}
	defer src.Close()

	if src.Size() != int64(len(want)) {
		t.Errorf("Size = %d, want %d", src.Size(), len(want))
	}
	if src.SectorSize() != DefaultSectorSize {
		t.Errorf("SectorSize = %d, want %d", src.SectorSize(), DefaultSectorSize)
	}
	if src.Name() != path {
		t.Errorf("Name = %q, want %q", src.Name(), path)
	}

	buf := make([]byte, 5)
	if _, err := src.ReadAt(buf, 6); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf) != "FINDI" {
		t.Errorf("ReadAt(6) = %q, want %q", buf, "FINDI")
	}

	r := Reader(src)
	all := make([]byte, len(want))
	if _, err := r.ReadAt(all, 0); err != nil {
		t.Fatalf("Reader.ReadAt: %v", err)
	}
	if string(all) != string(want) {
		t.Errorf("full read = %q, want %q", all, want)
	}
}

func TestOpenImage_Errors(t *testing.T) {
	if _, err := OpenImage(filepath.Join(t.TempDir(), "missing.bin")); err == nil {
		t.Error("expected error for missing file")
	}
	if _, err := OpenImage(t.TempDir()); err == nil {
		t.Error("expected error for directory")
	}
}
