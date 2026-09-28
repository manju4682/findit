// Package imaging copies a source to a `.bin` image. It streams fixed blocks
// with progress and, on read errors, either aborts or zero-fills and records
// the bad region so a failing drive still yields a usable image.
package imaging

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/findit/findit/internal/jobs"
	"github.com/findit/findit/internal/storage"
)

// ErrorPolicy decides what happens when a source block cannot be read.
type ErrorPolicy int

const (
	Abort    ErrorPolicy = iota // stop and fail on the first read error
	ZeroFill                    // zero-fill the bad block, record it, continue
)

// DefaultBlockSize is the copy granularity when Options.BlockSize is unset.
const DefaultBlockSize = 4 << 20 // 4 MiB

// Region is a byte range that could not be read from the source.
type Region struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

// Result summarizes a completed imaging run.
type Result struct {
	DestPath    string   `json:"destPath"`
	BytesCopied int64    `json:"bytesCopied"`
	BadRegions  []Region `json:"badRegions,omitempty"`
}

// Options configures an imaging run.
type Options struct {
	BlockSize        int
	OnReadError      ErrorPolicy
	ProgressInterval time.Duration
}

// Job is a running imaging operation: a Job plus its Result once finished.
type Job struct {
	*jobs.Job
	mu     sync.Mutex
	result Result
}

// Result returns the imaging result (valid after the job completes).
func (j *Job) Result() Result {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.result
}

func (j *Job) setResult(r Result) {
	j.mu.Lock()
	j.result = r
	j.mu.Unlock()
}

// Image copies src to destPath as a raw image, returning a running Job. It
// refuses a destination equal to the source. Consumers drain Events() then read
// Result().
func Image(parent context.Context, src storage.Source, destPath string, opts Options) (*Job, error) {
	srcAbs, _ := filepath.Abs(src.Name())
	dstAbs, _ := filepath.Abs(destPath)
	if srcAbs == dstAbs {
		return nil, fmt.Errorf("imaging: destination must differ from the source %q", src.Name())
	}
	bs := opts.BlockSize
	if bs <= 0 {
		bs = DefaultBlockSize
	}
	interval := opts.ProgressInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}

	j := &Job{}
	j.Job = jobs.Run(parent, 32, func(ctx context.Context, emit func(jobs.Event)) error {
		out, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("imaging: creating destination: %w", err)
		}
		defer out.Close()

		size := src.Size()
		buf := make([]byte, bs)
		var copied int64
		var bad []Region
		start := time.Now()
		lastEmit := time.Time{}

		emit(jobs.Event{Kind: jobs.KindProgress, Phase: "image", Total: int(size / (1 << 20)),
			Message: "Creating a safe copy of the drive…"})

		for off := int64(0); off < size; {
			if err := ctx.Err(); err != nil {
				return err
			}
			want := bs
			if off+int64(want) > size {
				want = int(size - off)
			}
			n, rerr := src.ReadAt(buf[:want], off)

			eff := n
			if rerr != nil && rerr != io.EOF {
				if opts.OnReadError == Abort {
					return fmt.Errorf("imaging: read error at offset %d: %w", off, rerr)
				}
				for i := 0; i < want; i++ {
					buf[i] = 0
				}
				eff = want
				bad = appendRegion(bad, off, int64(want))
				emit(jobs.Event{Kind: jobs.KindLog, Phase: "image",
					Message: fmt.Sprintf("Unreadable area at %d (%d bytes) — filled with zeros and continuing.", off, want)})
			}
			if eff == 0 {
				break
			}
			if _, werr := out.WriteAt(buf[:eff], off); werr != nil {
				return fmt.Errorf("imaging: writing destination: %w", werr)
			}
			copied += int64(eff)
			off += int64(eff)

			if time.Since(lastEmit) >= interval {
				emitProgress(emit, copied, size, start)
				lastEmit = time.Now()
			}
			if rerr == io.EOF {
				break
			}
		}

		emitProgress(emit, copied, size, start)
		j.setResult(Result{DestPath: destPath, BytesCopied: copied, BadRegions: bad})
		return nil
	})
	return j, nil
}

func emitProgress(emit func(jobs.Event), copied, size int64, start time.Time) {
	elapsed := time.Since(start).Seconds()
	var mbps float64
	if elapsed > 0 {
		mbps = float64(copied) / (1 << 20) / elapsed
	}
	emit(jobs.Event{
		Kind: jobs.KindProgress, Phase: "image",
		Done: int(copied / (1 << 20)), Total: int(size / (1 << 20)),
		Message: fmt.Sprintf("%d / %d MB (%.0f MB/s)", copied/(1<<20), size/(1<<20), mbps),
	})
}

// appendRegion appends a bad region, merging it with the previous one when they
// are contiguous.
func appendRegion(regions []Region, off, length int64) []Region {
	if n := len(regions); n > 0 && regions[n-1].Offset+regions[n-1].Length == off {
		regions[n-1].Length += length
		return regions
	}
	return append(regions, Region{Offset: off, Length: length})
}
