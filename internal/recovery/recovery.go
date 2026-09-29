// Package recovery is the backend facade the UI calls. It ties together device
// discovery, imaging, scanning, preview, and extraction, routing preview and
// extraction to the right mechanism per source (raw = byte extents, filesystem
// = TSK icat).
package recovery

import (
	"context"
	"errors"
	"io"

	"github.com/manju4682/findit/internal/device"
	"github.com/manju4682/findit/internal/engines/tsk"
	"github.com/manju4682/findit/internal/extract"
	"github.com/manju4682/findit/internal/imaging"
	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/preview"
	"github.com/manju4682/findit/internal/session"
	"github.com/manju4682/findit/internal/storage"
)

// ListDevices returns the physical storage devices on this machine.
func ListDevices(ctx context.Context) ([]device.Device, error) {
	return device.List(ctx)
}

// OpenImage opens a disk image read-only.
func OpenImage(path string) (storage.Source, error) {
	return storage.OpenImage(path)
}

// OpenDevice opens a device read-only (image-first callers should image it via
// ImageDevice first).
func OpenDevice(d device.Device) (storage.Source, error) {
	return device.OpenReadOnly(d)
}

// ImageDevice copies a device to a .bin image, returning a running job.
func ImageDevice(ctx context.Context, d device.Device, destPath string, opts imaging.Options) (*imaging.Job, error) {
	src, err := device.OpenReadOnly(d)
	if err != nil {
		return nil, err
	}
	job, err := imaging.Image(ctx, src, destPath, opts)
	if err != nil {
		src.Close()
		return nil, err
	}
	go func() { _ = job.Wait(); src.Close() }()
	return job, nil
}

// Scan starts a recovery scan over src.
func Scan(ctx context.Context, src storage.Source, req model.ScanRequest) *session.Scan {
	return session.StartScan(ctx, src, req)
}

// Preview generates a thumbnail/status for one file, sourcing its bytes the
// right way for its source kind.
func Preview(ctx context.Context, src storage.Source, source model.RecoverySource, f model.RecoveredFile, maxDim int) *preview.Preview {
	if source.Kind == model.SourceFilesystem {
		rc, err := fsOpener(src, source)(ctx, f)
		if err != nil {
			return &preview.Preview{Kind: preview.KindNone, Status: model.StatusPartial, Note: "could not read file"}
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, previewCap))
		if err != nil {
			return &preview.Preview{Kind: preview.KindNone, Status: model.StatusPartial, Note: "could not read file"}
		}
		return preview.FromBytes(ctx, f.Ext, data, maxDim)
	}
	return preview.Generate(ctx, src, f, maxDim)
}

// Recover extracts the given files from a source to destDir, routing each file
// through the correct mechanism for its source kind.
func Recover(ctx context.Context, src storage.Source, source model.RecoverySource, files []model.RecoveredFile, destDir string, preservePaths bool) (*extract.Result, error) {
	// Writing to the drive being recovered could overwrite the very data we're after.
	if device.OnSourceDisk(src, destDir) {
		return nil, errors.New("recovered files can’t be saved on the drive you’re recovering from — choose a folder on a different drive")
	}
	var open extract.Opener
	if source.Kind == model.SourceFilesystem {
		open = fsOpener(src, source)
	} else {
		open = extract.ExtentOpener(src)
	}
	return extract.Extract(ctx, open, extract.Request{
		Files:             files,
		DestDir:           destDir,
		PreservePaths:     preservePaths,
		ProtectSourcePath: src.Name(),
	}, nil)
}

const previewCap = 64 << 20 // cap bytes pulled for a filesystem-file preview

// fsOpener streams a filesystem file's content via TSK icat (through the
// privileged helper when the drive is being scanned directly).
func fsOpener(src storage.Source, source model.RecoverySource) extract.Opener {
	eng := tsk.ForSource(src, source.Offset, source.FSType)
	return func(ctx context.Context, f model.RecoveredFile) (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			pw.CloseWithError(eng.ExtractFile(ctx, f.Ref, pw))
		}()
		return pr, nil
	}
}
