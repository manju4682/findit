// Package bytescan streams a reader in overlapping windows so callers can find
// signatures anywhere in a multi-gigabyte image without loading it into memory
// or missing matches that straddle a window boundary.
package bytescan

import (
	"bytes"
	"context"
	"io"
)

const chunkSize = 1 << 20 // 1 MiB read granularity

// Scan streams r in overlapping windows. For each window it calls visit with:
//   - win:  the window bytes (owned region followed by the carried overlap)
//   - base: the absolute offset of win[0] within r
//   - safe: the length of the owned region; visit must only report matches that
//     start at an index < safe. Bytes in win[safe:] reappear at the start of the
//     next window (with the correct base), so a match spanning the boundary is
//     found there instead.
//
// overlap is the number of trailing bytes carried to the next window and must
// exceed the longest pattern (or fixed-size structure) the visitor inspects at a
// single position. win is reused between calls, so visit must not retain it.
// Scan stops promptly and returns ctx.Err() if ctx is canceled.
func Scan(ctx context.Context, r io.Reader, overlap int, visit func(win []byte, base int64, safe int)) error {
	overlap = max(overlap, 0)
	buf := make([]byte, overlap+chunkSize)
	carry := 0
	var base int64

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(r, buf[carry:carry+chunkSize])
		eof := err == io.EOF || err == io.ErrUnexpectedEOF
		if err != nil && !eof {
			return err
		}

		win := buf[:carry+n]
		safe := len(win)
		if !eof {
			safe = max(len(win)-overlap, 0)
		}

		visit(win, base, safe)

		if eof {
			return nil
		}
		base += int64(safe)
		carry = copy(buf, win[safe:])
	}
}

// IndexAll returns the start positions of every non-overlapping occurrence of
// sig within b.
func IndexAll(b, sig []byte) []int {
	var out []int
	for i := 0; ; {
		j := bytes.Index(b[i:], sig)
		if j < 0 {
			break
		}
		p := i + j
		out = append(out, p)
		i = p + len(sig)
	}
	return out
}
