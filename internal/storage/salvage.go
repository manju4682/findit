package storage

import (
	"errors"
	"io"
)

// SalvageChunk is the granularity at which a failed read is retried; a bad
// sector costs this much data instead of the whole read.
const SalvageChunk = 64 << 10

// Salvage re-reads p at off in SalvageChunk pieces after a failed read,
// zero-filling whatever still can't be read and reporting each such range to
// bad. Bytes that can be read are kept.
func Salvage(r io.ReaderAt, p []byte, off int64, bad func(off, length int64)) {
	for i := 0; i < len(p); i += SalvageChunk {
		end := min(i+SalvageChunk, len(p))
		n, err := r.ReadAt(p[i:end], off+int64(i))
		if err == nil || (errors.Is(err, io.EOF) && n == end-i) {
			continue
		}
		clear(p[i+n : end])
		if bad != nil {
			bad(off+int64(i+n), int64(end-i-n))
		}
	}
}

// Tolerant wraps src so read errors are salvaged instead of returned, for
// passes that must get through a failing drive. Unreadable ranges read as
// zeros and are reported to bad.
func Tolerant(src Source, bad func(off, length int64)) io.ReaderAt {
	return tolerant{src, bad}
}

type tolerant struct {
	src Source
	bad func(off, length int64)
}

func (t tolerant) ReadAt(p []byte, off int64) (int, error) {
	n, err := t.src.ReadAt(p, off)
	if err == nil || errors.Is(err, io.EOF) || off >= t.src.Size() {
		return n, err
	}
	want := int(min(int64(len(p)), t.src.Size()-off))
	Salvage(t.src, p[:want], off, t.bad)
	if want < len(p) {
		return want, io.EOF
	}
	return want, nil
}
