// Package device enumerates physical storage devices and opens them read-only.
// It is FindIt's only OS-specific surface; downstream code just consumes a
// storage.Source.
package device

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/manju4682/findit/internal/engines/tsk"
	"github.com/manju4682/findit/internal/storage"
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

// Volume describes the mounted volume holding a path.
type Volume struct {
	Disk   string // physical whole disk, e.g. "disk4"; "" if unknown
	FSType string // e.g. "apfs", "msdos", "exfat"
	Free   int64  // bytes available to the user
}

// VolumeOf reports the volume holding path. If path doesn't exist yet, its
// nearest existing ancestor is used.
func VolumeOf(path string) (Volume, error) {
	p := filepath.Clean(path)
	for {
		v, err := volumeOf(p)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return v, err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return Volume{}, err
		}
		p = parent
	}
}

const fat32MaxFile = 4<<30 - 1

// CheckImageDestination rejects a clone destination that can't work: one on
// the drive being copied (it would overwrite the data being recovered), on a
// volume without enough free space, or on FAT32 when the image exceeds 4 GB.
// If the volume can't be inspected, the destination is allowed.
func CheckImageDestination(dest string, d Device) error {
	if !filepath.IsAbs(dest) {
		return fmt.Errorf("choose a full destination path, not %q", dest)
	}
	v, err := VolumeOf(filepath.Dir(dest))
	if err != nil {
		return nil
	}
	if v.Disk != "" && v.Disk == d.ID {
		return errors.New("the copy can’t be saved on the drive you’re recovering from — that would overwrite the files you want back. Choose a folder on a different drive")
	}
	if v.FSType == "msdos" && d.Size > fat32MaxFile {
		return errors.New("the destination drive is formatted FAT32, which can’t hold files larger than 4 GB. Choose a drive formatted APFS, Mac OS Extended or exFAT")
	}
	if v.Free > 0 && v.Free < d.Size {
		if fi, err := os.Stat(dest); err == nil && fi.Mode().IsRegular() {
			v.Free += fi.Size() // an image being replaced frees its space
		}
	}
	if v.Free > 0 && v.Free < d.Size {
		return fmt.Errorf("not enough free space: the copy needs %s but the destination has %s available", humanBytes(d.Size), humanBytes(v.Free))
	}
	return nil
}

// OnSourceDisk reports whether path lives on the physical disk that src reads
// from directly. It is always false for disk images.
func OnSourceDisk(src storage.Source, path string) bool {
	ds, ok := src.(*deviceSource)
	if !ok || ds.id == "" {
		return false
	}
	v, err := VolumeOf(path)
	return err == nil && v.Disk == ds.id
}

// humanBytes formats n in decimal units, matching Finder.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// deviceSource is a read-only storage.Source backed by a raw device node.
type deviceSource struct {
	f      *os.File
	ra     *alignedReaderAt
	size   int64
	bs     int
	name   string
	id     string // whole-disk id, e.g. "disk4"
	helper Helper // set for directly-scanned drives; runs TSK as root
}

// Helper runs The Sleuth Kit against the device with elevated privileges and is
// shut down when the source closes.
type Helper interface {
	tsk.Executor
	io.Closer
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
		id:   d.ID,
	}, nil
}

func formatDeviceOpenError(path string, err error) string {
	msg := fmt.Sprintf("device: opening %s read-only: %v", path, err)
	if !isDevicePermissionError(err) {
		return msg
	}
	return msg + "\n\nReading a whole drive directly requires administrator privileges, which macOS does not grant through Full Disk Access (that only covers your personal files, not raw disks). Use “Make a safe copy” to clone the drive first — it will prompt for your password — then scan the copy. Working from a clone is also safer for a failing drive."
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
func (s *deviceSource) Size() int64                             { return s.size }
func (s *deviceSource) SectorSize() int                         { return s.bs }
func (s *deviceSource) Name() string                            { return s.name }

// Close releases the device and stops the privileged helper, if any.
func (s *deviceSource) Close() error {
	if s.helper != nil {
		_ = s.helper.Close()
	}
	return s.f.Close()
}

// TSKExecutor implements tsk.ExecutorProvider: TSK must go through the helper,
// because it reopens the disk itself and only root may do that.
func (s *deviceSource) TSKExecutor() tsk.Executor {
	if s.helper == nil {
		return nil
	}
	return s.helper
}

// NewHelperSource wraps a descriptor for device d received from a privileged
// helper as a read-only Source. The app reads the drive through f; TSK runs via
// helper.
func NewHelperSource(f *os.File, d Device, helper Helper) storage.Source {
	bs := d.BlockSize
	if bs <= 0 {
		bs = storage.DefaultSectorSize
	}
	return &deviceSource{
		f:      f,
		ra:     newAlignedReaderAt(f, bs, d.Size),
		size:   d.Size,
		bs:     bs,
		name:   d.RawNode,
		id:     d.ID,
		helper: helper,
	}
}
