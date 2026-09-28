package diagnosis

import (
	"encoding/binary"
	"io"
)

// MBR (Master Boot Record) layout constants, fixed by the DOS/MBR partitioning
// standard — not tunable values.
const (
	mbrSize            = 512  // a Master Boot Record is one 512-byte sector
	mbrSigOffset       = 510  // 2-byte boot signature at the end of the MBR
	mbrPartTableOffset = 446  // first partition entry starts here
	mbrEntrySize       = 16   // each partition entry is 16 bytes
	mbrEntryCount      = 4    // the MBR holds four primary entries
	ptypeEmpty         = 0x00 // unused partition entry
	ptypeGPTProtective = 0xEE // entry type marking a protective MBR over a GPT

	// Field offsets within a 16-byte MBR partition entry.
	mbrEntryTypeOff    = 4  // partition type byte
	mbrEntryLBAOff     = 8  // starting LBA (uint32)
	mbrEntrySectorsOff = 12 // length in sectors (uint32)
)

// GPT (GUID Partition Table) layout constants, fixed by the UEFI standard.
const (
	gptHdrEntryLBAOff   = 72  // uint64: LBA of the partition-entry array
	gptHdrNumEntriesOff = 80  // uint32: number of entries
	gptHdrEntrySizeOff  = 84  // uint32: size of each entry
	gptMaxEntries       = 128 // hard cap on entries we read
	gptEntrySizeMin     = 128 // smallest valid entry size
	gptEntrySizeMax     = 512 // largest entry size we accept
	gptEntryTypeGUIDLen = 16  // leading type-GUID; all-zero means unused
	gptEntryFirstLBAOff = 32  // uint64: first LBA of the partition
	gptEntryLastLBAOff  = 40  // uint64: last LBA (inclusive)
)

var (
	mbrSignature = []byte{0x55, 0xAA}
	gptSignature = []byte("EFI PART")
)

// partition is a byte range within the image that a partition table points to.
type partition struct {
	offset int64
	size   int64
}

// parsePartitions reads the MBR partition table, following a protective MBR into
// a GPT. Returns nil when there is no partition table (the filesystem is at the
// start of the image).
func parsePartitions(r io.ReaderAt, sectorSize int64) []partition {
	mbr := make([]byte, mbrSize)
	if _, err := r.ReadAt(mbr, 0); err != nil {
		return nil
	}
	if mbr[mbrSigOffset] != mbrSignature[0] || mbr[mbrSigOffset+1] != mbrSignature[1] {
		return nil
	}

	var parts []partition
	for i := 0; i < mbrEntryCount; i++ {
		e := mbr[mbrPartTableOffset+i*mbrEntrySize : mbrPartTableOffset+i*mbrEntrySize+mbrEntrySize]
		typ := e[mbrEntryTypeOff]
		if typ == ptypeGPTProtective { // protective MBR -> GPT
			return parseGPT(r, sectorSize)
		}
		lba := int64(binary.LittleEndian.Uint32(e[mbrEntryLBAOff : mbrEntryLBAOff+4]))
		sectors := int64(binary.LittleEndian.Uint32(e[mbrEntrySectorsOff : mbrEntrySectorsOff+4]))
		if typ != ptypeEmpty && lba > 0 {
			parts = append(parts, partition{offset: lba * sectorSize, size: sectors * sectorSize})
		}
	}
	return parts
}

func parseGPT(r io.ReaderAt, sectorSize int64) []partition {
	hdr := make([]byte, mbrSize)
	if _, err := r.ReadAt(hdr, sectorSize); err != nil {
		return nil
	}
	if string(hdr[0:len(gptSignature)]) != string(gptSignature) {
		return nil
	}
	entryLBA := int64(binary.LittleEndian.Uint64(hdr[gptHdrEntryLBAOff : gptHdrEntryLBAOff+8]))
	numEntries := int64(binary.LittleEndian.Uint32(hdr[gptHdrNumEntriesOff : gptHdrNumEntriesOff+4]))
	entrySize := int64(binary.LittleEndian.Uint32(hdr[gptHdrEntrySizeOff : gptHdrEntrySizeOff+4]))
	if numEntries > gptMaxEntries {
		numEntries = gptMaxEntries
	}
	if entrySize < gptEntrySizeMin || entrySize > gptEntrySizeMax {
		entrySize = gptEntrySizeMin
	}

	var parts []partition
	buf := make([]byte, entrySize)
	for i := int64(0); i < numEntries; i++ {
		if _, err := r.ReadAt(buf, entryLBA*sectorSize+i*entrySize); err != nil {
			break
		}
		if allZero(buf[0:gptEntryTypeGUIDLen]) { // empty type GUID
			continue
		}
		first := int64(binary.LittleEndian.Uint64(buf[gptEntryFirstLBAOff : gptEntryFirstLBAOff+8]))
		last := int64(binary.LittleEndian.Uint64(buf[gptEntryLastLBAOff : gptEntryLastLBAOff+8]))
		if first > 0 && last >= first {
			parts = append(parts, partition{offset: first * sectorSize, size: (last - first + 1) * sectorSize})
		}
	}
	return parts
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
