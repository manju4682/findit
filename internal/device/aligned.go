package device

import "io"

// alignedReaderAt adapts a block-oriented reader (like a raw /dev/rdiskN, which
// only accepts reads aligned to its block size) into an io.ReaderAt that serves
// arbitrary offsets and lengths. It reads the enclosing aligned block range and
// slices out the requested bytes.
type alignedReaderAt struct {
	ra   io.ReaderAt
	bs   int64
	size int64
}

func newAlignedReaderAt(ra io.ReaderAt, blockSize int, size int64) *alignedReaderAt {
	if blockSize <= 0 {
		blockSize = 512
	}
	return &alignedReaderAt{ra: ra, bs: int64(blockSize), size: size}
}

func (a *alignedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, io.EOF
	}
	if off >= a.size {
		return 0, io.EOF
	}
	end := off + int64(len(p))
	if end > a.size {
		end = a.size
	}
	start := off - off%a.bs
	alignedEnd := ((end + a.bs - 1) / a.bs) * a.bs

	buf := make([]byte, alignedEnd-start)
	n, err := a.ra.ReadAt(buf, start)
	avail := int64(n) - (off - start)
	if avail <= 0 {
		if err == nil {
			err = io.EOF
		}
		return 0, err
	}
	want := int64(len(p))
	if want > avail {
		want = avail
	}
	copy(p[:want], buf[off-start:off-start+want])
	if int(want) < len(p) {
		return int(want), io.EOF
	}
	return int(want), nil
}
