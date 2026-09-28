// Package extract writes recovered files to a destination. As the only
// component that writes user data, it refuses a destination on the source and
// sanitizes paths so nothing escapes the destination.
package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
)

// Request describes an extraction: which files to write, where, and how.
type Request struct {
	Files         []model.RecoveredFile
	DestDir       string
	PreservePaths bool // keep each file's per-source directory path
	// ProtectSourcePath, when set, refuses a destination equal to the source
	// image/device path.
	ProtectSourcePath string
	// ProtectDevicePath, when set, makes extraction refuse a destination on the
	// same physical device as this path (used for live-device recovery so we
	// never overwrite not-yet-recovered data). Empty for image-based recovery.
	ProtectDevicePath string
}

// Opener yields a file's bytes. It abstracts over the two recovery paths: raw
// files read from byte extents (ExtentOpener), filesystem files stream from an
// engine (e.g. TSK icat).
type Opener func(ctx context.Context, f model.RecoveredFile) (io.ReadCloser, error)

// ExtentOpener reads a file's bytes from its ByteRange extents in src.
func ExtentOpener(src storage.Source) Opener {
	return func(_ context.Context, f model.RecoveredFile) (io.ReadCloser, error) {
		if len(f.Extent) == 0 {
			return nil, errors.New("extract: file has no byte extent")
		}
		var rs []io.Reader
		for _, r := range f.Extent {
			rs = append(rs, io.NewSectionReader(src, r.Offset, r.Length))
		}
		return io.NopCloser(io.MultiReader(rs...)), nil
	}
}

// ItemResult is the outcome of writing one file.
type ItemResult struct {
	File  model.RecoveredFile
	Path  string // where it was written ("" on failure)
	Bytes int64
	Err   error
}

// Result summarizes an extraction run.
type Result struct {
	Written int
	Failed  int
	Items   []ItemResult
}

// Extract writes req.Files, sourcing each file's bytes through open, into
// req.DestDir. It reports per-file outcomes; a single file failure does not
// abort the run.
func Extract(ctx context.Context, open Opener, req Request, progress func(done, total int)) (*Result, error) {
	if req.DestDir == "" {
		return nil, errors.New("extract: destination directory is required")
	}
	destRoot, err := prepareDest(req)
	if err != nil {
		return nil, err
	}

	res := &Result{}
	total := len(req.Files)
	for i, f := range req.Files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		path, n, err := writeOne(ctx, open, destRoot, req.PreservePaths, f)
		res.Items = append(res.Items, ItemResult{File: f, Path: path, Bytes: n, Err: err})
		if err != nil {
			res.Failed++
		} else {
			res.Written++
		}
		if progress != nil {
			progress(i+1, total)
		}
	}
	return res, nil
}

// prepareDest validates and creates the destination, returning its absolute
// path. It enforces the source/destination safety guards.
func prepareDest(req Request) (string, error) {
	destRoot, err := filepath.Abs(req.DestDir)
	if err != nil {
		return "", err
	}
	if req.ProtectSourcePath != "" {
		if srcAbs, _ := filepath.Abs(req.ProtectSourcePath); destRoot == srcAbs {
			return "", fmt.Errorf("extract: destination must differ from the source %q", req.ProtectSourcePath)
		}
	}
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", fmt.Errorf("extract: creating destination: %w", err)
	}
	fi, err := os.Stat(destRoot)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("extract: destination %q is not a directory", destRoot)
	}
	if req.ProtectDevicePath != "" {
		same, err := sameDevice(destRoot, req.ProtectDevicePath)
		if err == nil && same {
			return "", fmt.Errorf("extract: destination is on the same device being recovered; choose another drive")
		}
	}
	return destRoot, nil
}

// writeOne writes a single recovered file and returns its path and byte count.
func writeOne(ctx context.Context, open Opener, destRoot string, preservePaths bool, f model.RecoveredFile) (string, int64, error) {
	rel := relPath(preservePaths, f)
	dest := filepath.Join(destRoot, rel)
	clean := filepath.Clean(dest)
	if clean != destRoot && !strings.HasPrefix(clean, destRoot+string(os.PathSeparator)) {
		return "", 0, fmt.Errorf("refusing to write outside destination: %q", rel)
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return "", 0, err
	}

	rc, err := open(ctx, f)
	if err != nil {
		return "", 0, err
	}
	defer rc.Close()

	out, err := os.Create(clean)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, rc)
	if err != nil {
		return clean, n, err
	}
	return clean, n, nil
}

// relPath computes the destination-relative path for a file, sanitized so it
// can never escape the destination.
func relPath(preservePaths bool, f model.RecoveredFile) string {
	name := f.Name
	if name == "" {
		if f.Ext != "" {
			name = fmt.Sprintf("%s.%s", f.ID, f.Ext)
		} else {
			name = f.ID
		}
	}
	if preservePaths && f.Path != "" {
		return filepath.Join(sanitize(f.Path), name)
	}
	return sanitize(name)
}

// sanitize strips drive letters, leading separators, and any ".." components so
// the result stays within the destination.
func sanitize(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	var parts []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".", "..":
			continue
		default:
			if i := strings.IndexByte(seg, ':'); i >= 0 { // strip "C:" style prefixes
				seg = seg[i+1:]
			}
			if seg != "" {
				parts = append(parts, seg)
			}
		}
	}
	return filepath.Join(parts...)
}
