//go:build unix

// Package privdev lets the app read a whole drive directly without itself
// running as root. A privileged helper (launched once via the macOS admin
// prompt) opens the device, passes the open descriptor back over a unix socket
// (SCM_RIGHTS) for the app's own reads, and then stays connected to run The
// Sleuth Kit as root on request — TSK must reopen the disk, which only root can
// do. The helper exits as soon as the app closes the session or quits.
//
// Wire protocol:
//   - control connection (helper dials the app): one SCM_RIGHTS message carrying
//     the device fd, then newline-delimited JSON requests from app to helper.
//   - per request, the helper dials a fresh data connection, writes the 8-byte
//     request id, then frames: 'D'+len+bytes (stdout), ending with 'X' (ok) or
//     'E'+len+message (error). Closing a data connection cancels that request.
package privdev

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/manju4682/findit/internal/engines/tsk"
)

const (
	frameData  = 'D'
	frameError = 'E'
	frameDone  = 'X'
	maxFrame   = 1 << 20
	chunkSize  = 64 << 10
)

// respondTimeout bounds how long a request waits for the helper to connect back.
const respondTimeout = 30 * time.Second

type wireReq struct {
	ID uint64 `json:"id"`
	tsk.Request
}

// ---- app side ---------------------------------------------------------------

// Listener waits for the privileged helper to connect back.
type Listener struct {
	l    *net.UnixListener
	path string
}

// Listen binds a unix socket at path (inside the user's private temp dir).
func Listen(path string) (*Listener, error) {
	_ = os.Remove(path)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	return &Listener{l: l, path: path}, nil
}

// Close stops listening and removes the socket file.
func (ln *Listener) Close() {
	_ = ln.l.Close()
	_ = os.Remove(ln.path)
}

// Accept waits up to timeout for the helper's control connection, receives the
// device descriptor (named name), and returns a Session for running TSK.
func (ln *Listener) Accept(name string, timeout time.Duration) (*Session, *os.File, error) {
	if timeout > 0 {
		_ = ln.l.SetDeadline(time.Now().Add(timeout))
	}
	c, err := ln.l.AcceptUnix()
	if err != nil {
		return nil, nil, err
	}
	f, err := recvFD(c, name)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	_ = ln.l.SetDeadline(time.Time{})
	s := &Session{ln: ln.l, ctrl: c, pending: map[uint64]chan *net.UnixConn{}, done: make(chan struct{})}
	go s.watchCtrl()
	go s.acceptLoop()
	return s, f, nil
}

// Session is a live connection to the privileged helper. It implements
// tsk.Executor, running each TSK request as root on the helper's device.
type Session struct {
	ln   *net.UnixListener
	ctrl *net.UnixConn

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan *net.UnixConn

	done      chan struct{} // closed when the helper goes away
	closeOnce sync.Once
}

// Close ends the session; the helper sees EOF and exits.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		_ = s.ctrl.Close()
		_ = s.ln.Close() // also unlinks the socket file
	})
	return nil
}

// Exec implements tsk.Executor.
func (s *Session) Exec(ctx context.Context, req tsk.Request, w io.Writer) error {
	ch := make(chan *net.UnixConn, 1)
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.pending[id] = ch
	line, _ := json.Marshal(wireReq{ID: id, Request: req})
	_, err := s.ctrl.Write(append(line, '\n'))
	s.mu.Unlock()
	if err != nil {
		s.drop(id)
		return fmt.Errorf("privileged helper unavailable: %w", err)
	}

	var c *net.UnixConn
	select {
	case c = <-ch:
	case <-ctx.Done():
		s.drop(id)
		return ctx.Err()
	case <-s.done:
		s.drop(id)
		return errors.New("privileged helper exited")
	case <-time.After(respondTimeout):
		s.drop(id)
		return errors.New("privileged helper did not respond")
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() }) // cancel mid-stream
	defer stop()

	if err := readFrames(c, w); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func (s *Session) drop(id uint64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// watchCtrl detects the helper exiting (the control connection closes).
func (s *Session) watchCtrl() {
	buf := make([]byte, 1)
	for {
		if _, err := s.ctrl.Read(buf); err != nil {
			close(s.done)
			return
		}
	}
}

// acceptLoop routes each data connection to the request that asked for it.
func (s *Session) acceptLoop() {
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		var idb [8]byte
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(c, idb[:]); err != nil {
			c.Close()
			continue
		}
		_ = c.SetReadDeadline(time.Time{})
		id := binary.BigEndian.Uint64(idb[:])
		s.mu.Lock()
		ch, ok := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if !ok {
			c.Close() // request was abandoned; closing cancels it helper-side
			continue
		}
		ch <- c
	}
}

func readFrames(c io.Reader, w io.Writer) error {
	var hdr [5]byte
	buf := make([]byte, chunkSize)
	for {
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return fmt.Errorf("privileged helper: %w", err)
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > maxFrame {
			return fmt.Errorf("privileged helper: oversized frame (%d bytes)", n)
		}
		if int(n) > len(buf) {
			buf = make([]byte, n)
		}
		payload := buf[:n]
		if _, err := io.ReadFull(c, payload); err != nil {
			return fmt.Errorf("privileged helper: %w", err)
		}
		switch hdr[0] {
		case frameData:
			if _, err := w.Write(payload); err != nil {
				return err
			}
		case frameDone:
			return nil
		case frameError:
			return errors.New(string(payload))
		default:
			return fmt.Errorf("privileged helper: unknown frame %q", hdr[0])
		}
	}
}

func recvFD(c *net.UnixConn, name string) (*os.File, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var ferr error
	rerr := raw.Read(func(s uintptr) bool {
		buf := make([]byte, 1)
		oob := make([]byte, unix.CmsgSpace(4))
		_, oobn, _, _, e := unix.Recvmsg(int(s), buf, oob, 0)
		if e == unix.EAGAIN || e == unix.EWOULDBLOCK {
			return false // not ready yet; wait for the poller
		}
		if e != nil {
			ferr = e
			return true
		}
		scms, e := unix.ParseSocketControlMessage(oob[:oobn])
		if e != nil || len(scms) == 0 {
			ferr = fmt.Errorf("privdev: no control message received")
			return true
		}
		fds, e := unix.ParseUnixRights(&scms[0])
		if e != nil || len(fds) == 0 {
			ferr = fmt.Errorf("privdev: no file descriptor received")
			return true
		}
		fd = fds[0]
		return true
	})
	if rerr != nil {
		return nil, rerr
	}
	if ferr != nil {
		return nil, ferr
	}
	return os.NewFile(uintptr(fd), name), nil
}

// ---- helper side ------------------------------------------------------------

// RunFunc executes one validated TSK request, streaming stdout to w.
type RunFunc func(ctx context.Context, req tsk.Request, w io.Writer) error

// Serve is the privileged helper's main loop. It opens fdPath read-only, passes
// the descriptor to the app over socketPath, then serves TSK requests with run
// until the app closes the control connection.
func Serve(socketPath, fdPath string, run RunFunc) error {
	f, err := os.OpenFile(fdPath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		f.Close()
		return err
	}
	defer c.Close()
	err = sendFD(c, int(f.Fd()))
	f.Close() // the app holds its own copy now
	if err != nil {
		return err
	}

	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var r wireReq
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		go serveOne(socketPath, r, run)
	}
	return nil
}

func serveOne(socketPath string, r wireReq, run RunFunc) {
	dc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return
	}
	defer dc.Close()
	var idb [8]byte
	binary.BigEndian.PutUint64(idb[:], r.ID)
	if _, err := dc.Write(idb[:]); err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fw := &frameWriter{c: dc, cancel: cancel}
	err = run(ctx, r.Request, fw)
	if fw.err != nil {
		return // the app hung up (canceled); nothing more to say
	}
	if err != nil {
		_ = writeFrame(dc, frameError, []byte(err.Error()))
		return
	}
	_ = writeFrame(dc, frameDone, nil)
}

// frameWriter wraps stdout into data frames; a write failure means the app
// canceled, so it cancels the running tool.
type frameWriter struct {
	c      io.Writer
	cancel context.CancelFunc
	err    error
}

func (fw *frameWriter) Write(p []byte) (int, error) {
	if fw.err != nil {
		return 0, fw.err
	}
	for off := 0; off < len(p); off += chunkSize {
		end := min(off+chunkSize, len(p))
		if err := writeFrame(fw.c, frameData, p[off:end]); err != nil {
			fw.err = err
			fw.cancel()
			return off, err
		}
	}
	return len(p), nil
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	hdr := [5]byte{typ}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

func sendFD(c *net.UnixConn, fd int) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	rights := unix.UnixRights(fd)
	var serr error
	werr := raw.Write(func(s uintptr) bool {
		e := unix.Sendmsg(int(s), []byte{0}, rights, nil, 0)
		if e == unix.EAGAIN || e == unix.EWOULDBLOCK {
			return false
		}
		serr = e
		return true
	})
	if werr != nil {
		return werr
	}
	return serr
}
