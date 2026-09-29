package imaging

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/manju4682/findit/internal/jobs"
	"github.com/manju4682/findit/internal/storage"
)

// memSource is an in-memory storage.Source with an optional unreadable range,
// used to exercise the imager without a real device.
type memSource struct {
	name             string
	data             []byte
	failFrom, failTo int64 // [failFrom, failTo) returns a read error; empty if failTo==0
}

func (m *memSource) ReadAt(p []byte, off int64) (int, error) {
	if m.failTo > 0 && off < m.failTo && off+int64(len(p)) > m.failFrom {
		return 0, errors.New("simulated I/O error")
	}
	if off >= int64(len(m.data)) {
		return 0, errors.New("eof")
	}
	n := copy(p, m.data[off:])
	return n, nil
}
func (m *memSource) Close() error    { return nil }
func (m *memSource) Size() int64     { return int64(len(m.data)) }
func (m *memSource) SectorSize() int { return 512 }
func (m *memSource) Name() string    { return m.name }

func drain(j *Job) {
	for range j.Events() {
	}
}

func TestImage_CopiesIdentical(t *testing.T) {
	data := make([]byte, 1<<20+1234) // not a block multiple
	rand.New(rand.NewSource(1)).Read(data)
	src := &memSource{name: "mem-src", data: data}
	dest := filepath.Join(t.TempDir(), "out.bin")

	j, err := Image(context.Background(), src, dest, Options{BlockSize: 64 << 10})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	var sawProgress bool
	for e := range j.Events() {
		if e.Kind == jobs.KindProgress {
			sawProgress = true
		}
	}
	if err := j.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !sawProgress {
		t.Error("expected progress events")
	}

	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Errorf("image differs from source (len got=%d want=%d)", len(got), len(data))
	}
	r := j.Result()
	if r.BytesCopied != int64(len(data)) || len(r.BadRegions) != 0 {
		t.Errorf("result = %+v, want full copy no bad regions", r)
	}
}

func TestImage_ZeroFillsBadBlocks(t *testing.T) {
	const bs = 64 << 10
	data := make([]byte, 4*bs)
	rand.New(rand.NewSource(2)).Read(data)
	// Make the third block unreadable.
	src := &memSource{name: "mem", data: data, failFrom: 2 * bs, failTo: 3 * bs}
	dest := filepath.Join(t.TempDir(), "out.bin")

	j, err := Image(context.Background(), src, dest, Options{BlockSize: bs, OnReadError: ZeroFill})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	drain(j)
	if err := j.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	got, _ := os.ReadFile(dest)
	if len(got) != len(data) {
		t.Fatalf("image size = %d, want %d", len(got), len(data))
	}
	// Bad block is zeros; surrounding blocks match the source.
	for i := 2 * bs; i < 3*bs; i++ {
		if got[i] != 0 {
			t.Fatalf("byte %d not zero-filled", i)
		}
	}
	if !bytes.Equal(got[:2*bs], data[:2*bs]) || !bytes.Equal(got[3*bs:], data[3*bs:]) {
		t.Error("readable regions were not copied correctly")
	}
	r := j.Result()
	if len(r.BadRegions) != 1 || r.BadRegions[0].Offset != 2*bs || r.BadRegions[0].Length != bs {
		t.Errorf("bad regions = %+v, want one [%d,%d]", r.BadRegions, 2*bs, bs)
	}
}

// TestImage_BadSectorLosesOnlyItsChunk: one bad sector inside a large block
// must not zero the whole block — the rest is salvaged by re-reading.
func TestImage_BadSectorLosesOnlyItsChunk(t *testing.T) {
	data := make([]byte, DefaultBlockSize)
	rand.New(rand.NewSource(3)).Read(data)
	const badAt = 1<<20 + 4096
	src := &memSource{name: "mem", data: data, failFrom: badAt, failTo: badAt + 512}
	dest := filepath.Join(t.TempDir(), "out.bin")

	j, err := Image(context.Background(), src, dest, Options{OnReadError: ZeroFill})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	drain(j)
	if err := j.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	r := j.Result()
	chunk := int64(storage.SalvageChunk)
	lost := badAt / chunk * chunk
	if len(r.BadRegions) != 1 || r.BadRegions[0].Offset != lost || r.BadRegions[0].Length != chunk {
		t.Fatalf("bad regions = %+v, want one [%d,%d]", r.BadRegions, lost, chunk)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got[:lost], data[:lost]) || !bytes.Equal(got[lost+chunk:], data[lost+chunk:]) {
		t.Error("readable data around the bad sector was not preserved")
	}
}

func TestImage_AbortPolicyFails(t *testing.T) {
	const bs = 64 << 10
	data := make([]byte, 4*bs)
	src := &memSource{name: "mem", data: data, failFrom: bs, failTo: 2 * bs}
	dest := filepath.Join(t.TempDir(), "out.bin")

	j, err := Image(context.Background(), src, dest, Options{BlockSize: bs, OnReadError: Abort})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	drain(j)
	if err := j.Wait(); err == nil {
		t.Fatal("expected imaging to fail under Abort policy")
	}
}

func TestImage_RejectsDestEqualsSource(t *testing.T) {
	src := &memSource{name: "/tmp/same.bin", data: []byte{1, 2, 3}}
	if _, err := Image(context.Background(), src, "/tmp/same.bin", Options{}); err == nil {
		t.Fatal("expected rejection when destination equals source")
	}
}
