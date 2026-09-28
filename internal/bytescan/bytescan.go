// Package bytescan streams a reader in overlapping windows so callers can find
// signatures anywhere in a multi-gigabyte image without loading it into memory
// or missing matches that straddle a window boundary.
package bytescan

import (
	"bufio"
	"bytes"
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
// single position.
func Scan(r io.Reader, overlap int, visit func(win []byte, base int64, safe int)) error {
	br := bufio.NewReaderSize(r, chunkSize)
	tmp := make([]byte, chunkSize)
	var carry []byte
	var base int64

	for {
		n, err := io.ReadFull(br, tmp)
		eof := err == io.EOF || err == io.ErrUnexpectedEOF
		if err != nil && !eof {
			return err
		}

		win := append(carry, tmp[:n]...)
		L := len(win)
		safe := L
		if !eof {
			if safe = L - overlap; safe < 0 {
				safe = 0
			}
		}

		visit(win, base, safe)

		if eof {
			return nil
		}
		base += int64(safe)
		carry = append([]byte(nil), win[safe:]...) // fresh copy; win may alias carry
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
