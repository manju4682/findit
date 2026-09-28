// Package carve recovers files directly from raw bytes, independent of any
// filesystem. It is data-driven: a registry maps a file extension to a
// Signature (how to detect and measure that type), and a raw scan carves only
// the extensions the caller requests. New types are added by registering a
// Signature — the scan loop never changes, and nothing is hardcoded into it.
package carve

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.Decode
	_ "image/png"  // register PNG decoder for image.Decode
	"io"
	"sort"

	"github.com/findit/findit/internal/bytescan"
	"github.com/findit/findit/internal/config"
	"github.com/findit/findit/internal/storage"
)

// maxFileSize caps how far carving follows a single file, bounding work on
// corrupt or adversarial input.
const maxFileSize = 256 << 20 // 256 MiB

type measurer func(src storage.Source, size, off int64) (length int64, valid bool)

// verifier confirms that a measured file actually opens (not just that its
// structure is plausible). It runs only after measure has accepted a candidate.
type verifier func(src storage.Source, off, length int64) bool

// Signature describes how to detect and measure one file type.
type Signature struct {
	Ext         string
	Magic       []byte
	MagicOffset int // where Magic sits within the file (e.g. 4 for the MP4 ftyp box)
	measure     measurer
	verify      verifier // optional: confirms the measured file truly decodes
}

var registry = map[string]*Signature{}

// Register adds or replaces a signature in the registry, allowing new file
// types to be supported without changing the scanner.
func Register(s *Signature) { registry[s.Ext] = s }

func init() {
	Register(&Signature{Ext: "jpg", Magic: []byte{0xFF, 0xD8, 0xFF}, measure: measureJPEG, verify: verifyImage})
	Register(&Signature{Ext: "png", Magic: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, measure: measurePNG, verify: verifyImage})
	Register(&Signature{Ext: "mp4", Magic: []byte("ftyp"), MagicOffset: 4, measure: measureMP4})
	Register(&Signature{Ext: "pdf", Magic: []byte("%PDF-"), measure: measurePDF, verify: verifyPDF})
	Register(&Signature{Ext: "zip", Magic: []byte{0x50, 0x4B, 0x03, 0x04}, measure: measureZIP, verify: verifyZIP})
}

// Supported returns the sorted extensions the native carver understands. The UI
// presents these as the selectable raw file types.
func Supported() []string {
	out := make([]string, 0, len(registry))
	for e := range registry {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// Carved is one recovered file: its type, where it starts, how long it is, and
// whether it passed structural validation.
type Carved struct {
	Ext    string `json:"ext"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Valid  bool   `json:"valid"`
}

// Scan carves the requested extensions from src. Extensions with no registered
// signature are ignored. Results are ordered by offset and non-overlapping (a
// signature inside an already-measured file is skipped).
func Scan(ctx context.Context, src storage.Source, exts []string) ([]Carved, error) {
	active := activeSignatures(exts)
	if len(active) == 0 {
		return nil, nil
	}
	starts, err := findStarts(src, active)
	if err != nil {
		return nil, err
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].off < starts[j].off })

	size := src.Size()
	var out []Carved
	var coveredEnd int64
	for _, s := range starts {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if s.off < coveredEnd {
			continue // inside a file we already measured
		}
		length, valid := s.sig.measure(src, size, s.off)
		if length <= 0 {
			continue
		}
		// A structurally-measured file is only reported as recoverable if it also
		// verifies (e.g. images must actually decode). Files that fail here are
		// still listed, but as fragments, so the UI never over-promises.
		if valid && s.sig.verify != nil {
			valid = s.sig.verify(src, s.off, length)
		}
		out = append(out, Carved{Ext: s.sig.Ext, Offset: s.off, Length: length, Valid: valid})
		coveredEnd = s.off + length
	}
	return out, nil
}

func activeSignatures(exts []string) []*Signature {
	var active []*Signature
	seen := map[string]bool{}
	for _, e := range exts {
		if seen[e] {
			continue
		}
		if sig := registry[e]; sig != nil {
			active = append(active, sig)
			seen[e] = true
		}
	}
	return active
}

type start struct {
	sig *Signature
	off int64
}

// findStarts streams the source once to collect candidate start offsets for all
// active signatures.
func findStarts(src storage.Source, active []*Signature) ([]start, error) {
	overlap := 32
	for _, s := range active {
		if l := len(s.Magic) + s.MagicOffset + 8; l > overlap {
			overlap = l
		}
	}
	var starts []start
	err := bytescan.Scan(storage.Reader(src), overlap, func(win []byte, base int64, safe int) {
		region := win[:safe]
		for _, s := range active {
			for _, p := range bytescan.IndexAll(region, s.Magic) {
				if off := base + int64(p) - int64(s.MagicOffset); off >= 0 {
					starts = append(starts, start{s, off})
				}
			}
		}
	})
	return starts, err
}

// findLastSignature returns the absolute offset of the last occurrence of needle
// within [off, off+maxFileSize), or -1. Used to locate trailing markers (PDF
// %%EOF, ZIP end-of-central-directory).
func findLastSignature(src storage.Source, off, size int64, needle []byte) int64 {
	end := off + maxFileSize
	if end > size {
		end = size
	}
	sec := io.NewSectionReader(src, off, end-off)
	last := int64(-1)
	_ = bytescan.Scan(sec, len(needle)+4, func(win []byte, base int64, safe int) {
		for _, p := range bytescan.IndexAll(win[:safe], needle) {
			if abs := off + base + int64(p); abs > last {
				last = abs
			}
		}
	})
	return last
}

// measureJPEG walks from the SOI marker to the first EOI (FFD9). To reject
// coincidental FFD8FF…FFD9 byte sequences it requires a plausible first marker
// after the SOI and an SOS (start-of-scan, FFDA) marker before the EOI.
func measureJPEG(src storage.Source, size, off int64) (int64, bool) {
	var head [4]byte
	if _, err := src.ReadAt(head[:], off); err != nil {
		return 0, false
	}
	if head[0] != 0xFF || head[1] != 0xD8 || head[2] != 0xFF || !validFirstMarker(head[3]) {
		return 0, false
	}
	buf := make([]byte, 64<<10)
	pos := off + 2
	var prevFF, sawSOS bool
	for pos < size && pos-off < maxFileSize {
		n, err := src.ReadAt(buf, pos)
		if n <= 0 {
			break
		}
		for i := 0; i < n; i++ {
			c := buf[i]
			if prevFF {
				if c == 0xDA {
					sawSOS = true
				}
				if c == 0xD9 && sawSOS {
					return (pos + int64(i) + 1) - off, true
				}
			}
			prevFF = c == 0xFF
		}
		pos += int64(n)
		if err != nil {
			break
		}
	}
	return 0, false
}

// validFirstMarker reports whether b is a JPEG marker that legitimately follows
// the SOI (an APPn, DQT, SOFn, DHT, SOS, DRI, or comment marker).
func validFirstMarker(b byte) bool {
	switch b {
	case 0xDB, 0xC0, 0xC1, 0xC2, 0xC3, 0xC4, 0xDA, 0xDD, 0xDC, 0xFE:
		return true
	}
	return b >= 0xE0 && b <= 0xEF
}

// verifyImage confirms a measured image actually decodes, so a fragmented or
// partially-overwritten file whose structure merely looks valid is not reported
// as recoverable. Images larger than the configured cap are trusted on their
// structure alone, to keep scan time bounded.
func verifyImage(src storage.Source, off, length int64) bool {
	if cap := config.Get().Carve.MaxVerifyBytes; cap > 0 && length > cap {
		return true
	}
	if _, _, err := image.Decode(io.NewSectionReader(src, off, length)); err != nil {
		return false
	}
	return true
}

// verifyZIP confirms a carved archive's central directory parses (covering
// zip/docx/xlsx/jar), catching fragmented or truncated archives whose signatures
// survived but whose index did not. It reads the directory only, never
// decompressing, so it stays cheap.
func verifyZIP(src storage.Source, off, length int64) bool {
	if cap := config.Get().Carve.MaxVerifyBytes; cap > 0 && length > cap {
		return true
	}
	_, err := zip.NewReader(io.NewSectionReader(src, off, length), length)
	return err == nil
}

// verifyPDF checks a carved PDF has a version header and a cross-reference
// pointer (startxref) before its final %%EOF. This is a structural check, not a
// full render: it rejects fragments whose index is gone, but cannot prove every
// object in the body survived (there is no standard-library PDF parser).
func verifyPDF(src storage.Source, off, length int64) bool {
	var head [8]byte
	if _, err := src.ReadAt(head[:], off); err != nil {
		return false
	}
	if !bytes.HasPrefix(head[:], []byte("%PDF-1.")) && !bytes.HasPrefix(head[:], []byte("%PDF-2.")) {
		return false
	}
	tailLen := int64(2048)
	if tailLen > length {
		tailLen = length
	}
	tail := make([]byte, tailLen)
	if _, err := src.ReadAt(tail, off+length-tailLen); err != nil && err != io.EOF {
		return false
	}
	return bytes.Contains(tail, []byte("startxref")) && bytes.Contains(tail, []byte("%%EOF"))
}

// measurePNG walks the chunk list from the signature to the IEND chunk.
func measurePNG(src storage.Source, size, off int64) (int64, bool) {
	var sig [8]byte
	if _, err := src.ReadAt(sig[:], off); err != nil {
		return 0, false
	}
	for i, b := range []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A} {
		if sig[i] != b {
			return 0, false
		}
	}
	pos := off + 8
	for pos+8 <= size && pos-off < maxFileSize {
		var hdr [8]byte
		if _, err := src.ReadAt(hdr[:], pos); err != nil {
			break
		}
		length := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		next := pos + 8 + length + 4 // length + type + data + CRC
		if length < 0 || next > size || next <= pos {
			break
		}
		if typ == "IEND" {
			return next - off, true
		}
		pos = next
	}
	return 0, false
}

// measureMP4 sums consecutive ISO-BMFF boxes starting at the ftyp box. A file is
// only recoverable if it also carries a moov box: that is the index a player
// needs, so a fragment that stops before it (or never had it) is not playable.
func measureMP4(src storage.Source, size, off int64) (int64, bool) {
	pos := off
	boxes := 0
	sawMoov := false
	for pos+8 <= size && pos-off < maxFileSize {
		var hdr [8]byte
		if _, err := src.ReadAt(hdr[:], pos); err != nil {
			break
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := hdr[4:8]
		if boxes == 0 && string(typ) != "ftyp" {
			return 0, false
		}
		if !printableType(typ) {
			break
		}
		if string(typ) == "moov" {
			sawMoov = true
		}
		switch boxSize {
		case 1: // 64-bit largesize follows the header
			var ext [8]byte
			if _, err := src.ReadAt(ext[:], pos+8); err != nil {
				return finishMP4(pos, off, boxes, sawMoov)
			}
			boxSize = int64(binary.BigEndian.Uint64(ext[:]))
		case 0: // box extends to end of source
			boxSize = size - pos
		}
		if boxSize < 8 || pos+boxSize > size {
			break
		}
		pos += boxSize
		boxes++
	}
	return finishMP4(pos, off, boxes, sawMoov)
}

func finishMP4(pos, off int64, boxes int, sawMoov bool) (int64, bool) {
	if boxes >= 1 && pos > off {
		return pos - off, boxes >= 2 && sawMoov // ftyp + a moov index at minimum
	}
	return 0, false
}

func printableType(t []byte) bool {
	for _, c := range t {
		if c < 0x20 || c > 0x7E {
			return false
		}
	}
	return true
}

// measurePDF spans from the %PDF- header to the last %%EOF trailer.
func measurePDF(src storage.Source, size, off int64) (int64, bool) {
	var h [5]byte
	if _, err := src.ReadAt(h[:], off); err != nil || string(h[:]) != "%PDF-" {
		return 0, false
	}
	eof := findLastSignature(src, off, size, []byte("%%EOF"))
	if eof < 0 {
		return 0, false
	}
	end := eof + int64(len("%%EOF"))
	var tail [2]byte
	if _, err := src.ReadAt(tail[:], end); err == nil {
		switch {
		case tail[0] == '\r':
			end++
			if tail[1] == '\n' {
				end++
			}
		case tail[0] == '\n':
			end++
		}
	}
	if end > size {
		end = size
	}
	return end - off, true
}

// measureZIP spans from the local-file-header signature to the end of the
// end-of-central-directory record (covering docx/xlsx/jar and plain zips).
func measureZIP(src storage.Source, size, off int64) (int64, bool) {
	var h [4]byte
	if _, err := src.ReadAt(h[:], off); err != nil || h[0] != 0x50 || h[1] != 0x4B || h[2] != 0x03 || h[3] != 0x04 {
		return 0, false
	}
	eocd := findLastSignature(src, off, size, []byte{0x50, 0x4B, 0x05, 0x06})
	if eocd < 0 {
		return 0, false
	}
	commentLen := 0
	var cl [2]byte
	if _, err := src.ReadAt(cl[:], eocd+20); err == nil {
		commentLen = int(binary.LittleEndian.Uint16(cl[:]))
	}
	end := eocd + 22 + int64(commentLen)
	if end > size {
		end = size
	}
	return end - off, true
}
