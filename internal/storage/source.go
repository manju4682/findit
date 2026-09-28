// Package storage is FindIt's single read-only access path to the bytes under
// recovery (a disk image or a device). Reading through a Source guarantees the
// origin is never modified.
package storage

import (
	"fmt"
	"io"
	"os"
)

// DefaultSectorSize is assumed when a source cannot report its own.
const DefaultSectorSize = 512

// Source is a read-only, random-access, sized view over recoverable bytes.
// Implementations must be safe for concurrent ReadAt calls (os.File is).
type Source interface {
	io.ReaderAt
	io.Closer

	// Size returns the total number of bytes addressable in the source.
	Size() int64

	// SectorSize returns the logical sector size in bytes.
	SectorSize() int

	// Name returns a human-readable identifier (e.g. the image path).
	Name() string
}

// FileImage is a Source backed by a regular file, typically a `.bin` disk image.
type FileImage struct {
	f          *os.File
	size       int64
	sectorSize int
	name       string
}

// OpenImage opens a disk image file read-only.
func OpenImage(path string) (*FileImage, error) {
	f, err := os.Open(path) // O_RDONLY
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if fi.IsDir() {
		f.Close()
		return nil, fmt.Errorf("storage: %q is a directory, not a disk image", path)
	}
	return &FileImage{f: f, size: fi.Size(), sectorSize: DefaultSectorSize, name: path}, nil
}

// ReadAt implements io.ReaderAt.
func (im *FileImage) ReadAt(p []byte, off int64) (int, error) { return im.f.ReadAt(p, off) }

// Close releases the underlying file handle.
func (im *FileImage) Close() error { return im.f.Close() }

// Size returns the image size in bytes.
func (im *FileImage) Size() int64 { return im.size }

// SectorSize returns the logical sector size in bytes.
func (im *FileImage) SectorSize() int { return im.sectorSize }

// Name returns the image path.
func (im *FileImage) Name() string { return im.name }

// Reader returns a fresh streaming reader over the entire source. Callers that
// need a sequential pass (e.g. the diagnosis scanner) use this instead of
// managing offsets by hand. Each call returns an independent reader.
func Reader(s Source) *io.SectionReader {
	return io.NewSectionReader(s, 0, s.Size())
}
