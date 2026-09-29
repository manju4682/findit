//go:build unix

package privdev

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manju4682/findit/internal/engines/tsk"
)

// startSession runs the helper side (Serve) against an ordinary file and returns
// the app-side session plus the received descriptor. No root or device needed.
func startSession(t *testing.T, content []byte, run RunFunc) (*Session, *os.File, chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pd") // short path: unix socket paths are length-limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dev := filepath.Join(dir, "dev.bin")
	if err := os.WriteFile(dev, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- Serve(sock, dev, run) }()

	s, f, err := ln.Accept("received", 5*time.Second)
	if err != nil {
		ln.Close()
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { s.Close(); f.Close() })
	return s, f, served
}

// TestSession_PassesDescriptorAndRunsRequests covers the whole protocol: the
// device descriptor is readable app-side, requests stream their output back,
// helper errors surface, and closing the session stops the helper.
func TestSession_PassesDescriptorAndRunsRequests(t *testing.T) {
	want := []byte("hello descriptor passing — findit")
	run := func(ctx context.Context, req tsk.Request, w io.Writer) error {
		if req.Op == "icat" && req.Inode == "bad" {
			return errors.New("icat: no such inode")
		}
		// Larger than one frame chunk, to exercise frame splitting.
		_, err := w.Write(bytes.Repeat([]byte(req.Op+"|"), 40000))
		return err
	}
	s, f, served := startSession(t, want, run)

	got := make([]byte, len(want))
	if _, err := f.ReadAt(got, 0); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q (err %v) through passed fd, want %q", got, err, want)
	}

	var out bytes.Buffer
	if err := s.Exec(context.Background(), tsk.Request{Op: "fls"}, &out); err != nil {
		t.Fatalf("Exec(fls): %v", err)
	}
	if out.String() != strings.Repeat("fls|", 40000) {
		t.Fatalf("Exec(fls) streamed %d bytes, want %d", out.Len(), 4*40000)
	}

	err := s.Exec(context.Background(), tsk.Request{Op: "icat", Inode: "bad"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no such inode") {
		t.Fatalf("Exec(bad inode) err = %v, want helper error", err)
	}

	s.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit after the session closed")
	}
}

// TestSession_CancelStopsRequest proves canceling a request returns promptly and
// cancels the tool running helper-side.
func TestSession_CancelStopsRequest(t *testing.T) {
	stopped := make(chan struct{})
	run := func(ctx context.Context, req tsk.Request, w io.Writer) error {
		defer close(stopped)
		for {
			if _, err := w.Write([]byte("x")); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	s, _, _ := startSession(t, []byte("dev"), run)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	err := s.Exec(ctx, tsk.Request{Op: "fls"}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel was not prompt")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("helper-side run was not canceled")
	}
}
