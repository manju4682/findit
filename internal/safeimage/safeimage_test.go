package safeimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

func TestDecode_Valid(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	img, err := Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 4 || b.Dy() != 3 {
		t.Fatalf("bounds = %v, want 4x3", b)
	}
}

// TestDecode_RejectsForgedDimensions feeds a PNG whose header claims
// 65535×65535 pixels; it must be refused without attempting the allocation.
func TestDecode_RejectsForgedDimensions(t *testing.T) {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 65535)
	binary.BigEndian.PutUint32(ihdr[4:8], 65535)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA

	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))

	if _, err := Decode(bytes.NewReader(b.Bytes())); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
