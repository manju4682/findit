// Package diagnosis identifies the current filesystem, finds evidence of
// previous/underlying filesystems, and counts raw media — in one streaming pass
// that scales to multi-gigabyte drives.
package diagnosis

import (
	"bytes"
	"io"
	"strings"

	"github.com/findit/findit/internal/bytescan"
	"github.com/findit/findit/internal/config"
	"github.com/findit/findit/internal/model"
)

const sectorSize = 512

// Boot-sector layout offsets, fixed by the FAT/exFAT/NTFS on-disk formats.
const (
	bootMinLen    = 90  // shortest slice identifyBoot can inspect
	oemNameOffset = 3   // exFAT/NTFS OEM/type string starts here
	oemNameLen    = 8   // ...and is 8 bytes long
	fatTypeOffset = 82  // FAT32 "FAT32   " type string
	fatLabelStart = 71  // FAT32 volume label
	fatLabelEnd   = 82  // ...ends here
	bootSigOffset = 510 // 2-byte boot signature (0x55 0xAA)
)

var bootSignature = [2]byte{0x55, 0xAA}

// Boot-sector identity strings, read at fixed offsets within a 512-byte sector.
var (
	oemExFAT = []byte("EXFAT   ") // bytes[3:11]
	oemNTFS  = []byte("NTFS    ") // bytes[3:11]
	fatType  = []byte("FAT32   ") // bytes[82:90]
)

// Raw content signatures used for both filesystem remnants and file carving.
var (
	sigJPEG = []byte{0xFF, 0xD8, 0xFF}
	sigPNG  = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	sigFTYP = []byte("ftyp")  // MP4/MOV box type, appears at file offset 4
	sigMFT  = []byte("FILE0") // NTFS MFT record header

	// Filesystem type strings searched anywhere as weaker (remnant) evidence.
	fsTypeSigs = map[model.FSType][]byte{
		model.FSFAT32: fatType,
		model.FSExFAT: oemExFAT,
		model.FSNTFS:  oemNTFS,
	}
)

// identifyBoot inspects a candidate 512-byte boot sector and returns the
// filesystem it describes, plus a volume label when available.
func identifyBoot(b []byte) (model.FSType, string) {
	if len(b) < bootMinLen {
		return model.FSUnknown, ""
	}
	switch {
	case bytes.Equal(b[oemNameOffset:oemNameOffset+oemNameLen], oemExFAT):
		return model.FSExFAT, ""
	case bytes.Equal(b[oemNameOffset:oemNameOffset+oemNameLen], oemNTFS):
		return model.FSNTFS, ""
	case bytes.Equal(b[fatTypeOffset:fatTypeOffset+oemNameLen], fatType):
		return model.FSFAT32, strings.TrimRight(string(b[fatLabelStart:fatLabelEnd]), " \x00")
	}
	return model.FSUnknown, ""
}

// scanResult accumulates raw findings from the streaming pass.
type scanResult struct {
	boot     []model.Evidence
	typeHits map[model.FSType][]int64
	mftHits  []int64
	carve    map[string]int
}

// scan performs one streaming pass over r. curType is the current filesystem,
// excluded from candidate evidence so the drive's own structures aren't
// reported as a "previous" filesystem.
func scan(r io.Reader, curType model.FSType) (*scanResult, error) {
	res := &scanResult{
		typeHits: map[model.FSType][]int64{},
		carve:    map[string]int{"jpg": 0, "png": 0, "mp4": 0},
	}
	// overlap must exceed one sector so the aligned boot-sector probe can read a
	// full 512-byte sector starting anywhere within the owned region.
	overlap := config.Get().Diagnosis.ScanOverlapBytes
	err := bytescan.Scan(r, overlap, func(win []byte, base int64, safe int) {
		scanWindow(res, win, safe, base, curType)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// scanWindow processes window[:end], reporting absolute offsets as base+pos.
func scanWindow(res *scanResult, window []byte, end int, base int64, curType model.FSType) {
	region := window[:end]

	res.carve["jpg"] += len(bytescan.IndexAll(region, sigJPEG))
	res.carve["png"] += len(bytescan.IndexAll(region, sigPNG))
	res.carve["mp4"] += len(bytescan.IndexAll(region, sigFTYP))

	for fsType, sig := range fsTypeSigs {
		if fsType == curType {
			continue
		}
		for _, p := range bytescan.IndexAll(region, sig) {
			res.typeHits[fsType] = append(res.typeHits[fsType], base+int64(p))
		}
	}

	for _, p := range bytescan.IndexAll(region, sigMFT) {
		res.mftHits = append(res.mftHits, base+int64(p))
	}

	// Aligned boot-sector probe: a valid boot sector ends with 0x55AA.
	first := ((base + sectorSize - 1) / sectorSize) * sectorSize
	for a := first; a+sectorSize <= base+int64(end); a += sectorSize {
		off := int(a - base)
		b := window[off : off+sectorSize]
		if b[bootSigOffset] != bootSignature[0] || b[bootSigOffset+1] != bootSignature[1] {
			continue
		}
		if t, label := identifyBoot(b); t != model.FSUnknown && t != curType {
			res.boot = append(res.boot, model.Evidence{
				Kind: "boot-sector", FSType: t, Offset: a, Detail: label,
			})
		}
	}
}
