// Package device enumerates physical storage devices and opens them read-only.
// It is FindIt's only OS-specific surface; downstream code just consumes a
// storage.Source.
package device

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/findit/findit/internal/storage"
)

// Device describes a whole physical disk the user might recover from.
type Device struct {
	ID        string `json:"id"`        // e.g. "disk4"
	Node      string `json:"node"`      // buffered path, e.g. "/dev/disk4"
	RawNode   string `json:"rawNode"`   // raw path for fast reads, e.g. "/dev/rdisk4"
	Name      string `json:"name"`      // media/model name
	Size      int64  `json:"size"`      // bytes
	BlockSize int    `json:"blockSize"` // logical block size
	Removable bool   `json:"removable"` // USB/SD/external
	Protocol  string `json:"protocol"`  // e.g. "USB", "PCI-Express"
	Internal  bool   `json:"internal"`
}

// List returns the physical storage devices on this machine.
func List(ctx context.Context) ([]Device, error) {
	return listDevices(ctx)
}

// deviceSource is a read-only storage.Source backed by a raw device node.
type deviceSource struct {
	f    *os.File
	ra   *alignedReaderAt
	size int64
	bs   int
	name string
}

// OpenReadOnly opens a device read-only for imaging. Reading a raw device
// usually requires elevated privileges; a permission error is returned as-is so
// the caller can prompt the user.
func OpenReadOnly(d Device) (storage.Source, error) {
	path := d.RawNode
	if path == "" {
		path = d.Node
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("%s", formatDeviceOpenError(path, err))
	}
	bs := d.BlockSize
	if bs <= 0 {
		bs = storage.DefaultSectorSize
	}
	return &deviceSource{
		f:    f,
		ra:   newAlignedReaderAt(f, bs, d.Size),
		size: d.Size,
		bs:   bs,
		name: path,
	}, nil
}

func formatDeviceOpenError(path string, err error) string {
	msg := fmt.Sprintf("device: opening %s read-only: %v", path, err)
	if !isDevicePermissionError(err) {
		return msg
	}
	return msg + "\n\nmacOS blocked raw disk access. Open System Settings → Privacy & Security → Full Disk Access and allow FindIt, then retry. The built-in clone helper can also copy the drive with administrator privileges if you need a safe image-first workflow."
}

func isDevicePermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "operation not permitted") || strings.Contains(s, "permission denied")
}

func (s *deviceSource) ReadAt(p []byte, off int64) (int, error) { return s.ra.ReadAt(p, off) }
func (s *deviceSource) Close() error                            { return s.f.Close() }
func (s *deviceSource) Size() int64                             { return s.size }
func (s *deviceSource) SectorSize() int                         { return s.bs }
func (s *deviceSource) Name() string                            { return s.name }
