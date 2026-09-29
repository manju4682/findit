package storage

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// flakySource serves data but fails any read that touches [badOff, badOff+badLen).
type flakySource struct {
	data           []byte
	badOff, badLen int64
}

func (f *flakySource) ReadAt(p []byte, off int64) (int, error) {
	if off < f.badOff+f.badLen && off+int64(len(p)) > f.badOff {
		return 0, errors.New("input/output error")
	}
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (f *flakySource) Close() error    { return nil }
func (f *flakySource) Size() int64     { return int64(len(f.data)) }
func (f *flakySource) SectorSize() int { return 512 }
func (f *flakySource) Name() string    { return "flaky" }

func TestTolerant_ZeroFillsOnlyTheBadChunk(t *testing.T) {
	data := bytes.Repeat([]byte{0xAB}, 4*SalvageChunk)
	src := &flakySource{data: data, badOff: SalvageChunk + 100, badLen: 10}

	var badOff, badLen int64
	r := Tolerant(src, func(off, n int64) { badOff, badLen = off, n })

	got := make([]byte, len(data))
	if n, err := r.ReadAt(got, 0); err != nil || n != len(data) {
		t.Fatalf("ReadAt = (%d, %v), want (%d, nil)", n, err, len(data))
	}
	if badOff != SalvageChunk || badLen != SalvageChunk {
		t.Fatalf("bad range = (%d, %d), want (%d, %d)", badOff, badLen, SalvageChunk, SalvageChunk)
	}
	for i, b := range got {
		inBad := int64(i) >= badOff && int64(i) < badOff+badLen
		if (inBad && b != 0) || (!inBad && b != 0xAB) {
			t.Fatalf("byte %d = %#x (inBad=%v)", i, b, inBad)
		}
	}
}

func TestTolerant_PassesThroughEOF(t *testing.T) {
	src := &flakySource{data: []byte("abc"), badOff: 100, badLen: 1}
	buf := make([]byte, 8)
	n, err := Tolerant(src, nil).ReadAt(buf, 1)
	if n != 2 || !errors.Is(err, io.EOF) || string(buf[:n]) != "bc" {
		t.Fatalf("ReadAt = (%d, %v, %q), want (2, EOF, \"bc\")", n, err, buf[:n])
	}
}
