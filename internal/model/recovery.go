package model

import "time"

// SourceKind distinguishes a filesystem source (has a directory tree) from a raw
// carve source (a flat list of files found by content).
type SourceKind string

const (
	SourceFilesystem SourceKind = "filesystem"
	SourceRaw        SourceKind = "raw"
)

// ByteRange locates a run of a file's bytes within the image.
type ByteRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

// AssessmentStatus is the user-facing recoverability verdict for one file.
type AssessmentStatus string

const (
	StatusGood         AssessmentStatus = "Good"
	StatusPartial      AssessmentStatus = "Partial"
	StatusMetadataOnly AssessmentStatus = "MetadataOnly"
	StatusRawFragment  AssessmentStatus = "RawFragment"
	StatusUncertain    AssessmentStatus = "Uncertain"
)

// RecoveryAssessment grades a file across independent dimensions; the UI derives
// its status from the weakest relevant one (never over-promise).
type RecoveryAssessment struct {
	Metadata   int              `json:"metadata"`   // 0..100
	Allocation int              `json:"allocation"` // 0..100
	Content    int              `json:"content"`    // 0..100
	Physical   int              `json:"physical"`   // 0..100 (bad sectors under the file)
	Status     AssessmentStatus `json:"status"`
}

// RecoveredFile is one recoverable file belonging to exactly one source. Files
// are never merged across sources.
type RecoveredFile struct {
	ID          string             `json:"id"`
	SourceID    string             `json:"sourceId"`
	Name        string             `json:"name"`
	Path        string             `json:"path,omitempty"` // full path within the source; empty for raw
	Ext         string             `json:"ext,omitempty"`
	Size        int64              `json:"size"`
	Modified    time.Time          `json:"modified,omitempty"`
	Extent      []ByteRange        `json:"extent,omitempty"`
	Assessment  RecoveryAssessment `json:"assessment"`
	Previewable bool               `json:"previewable"`
	Recoverable bool               `json:"recoverable"`
	Deleted     bool               `json:"deleted,omitempty"`
	Ref         string             `json:"ref,omitempty"` // opaque engine handle (e.g. TSK inode) for filesystem extraction
}

// Node is a directory-tree node for filesystem sources. A node is either a
// directory (IsDir, Children) or a file (File set).
type Node struct {
	Name     string         `json:"name"`
	IsDir    bool           `json:"isDir"`
	File     *RecoveredFile `json:"file,omitempty"`
	Children []*Node        `json:"children,omitempty"`
}

// RecoverySource is one user-selectable scan target and its own, separate
// results. Filesystem sources expose Root (a tree); raw sources expose Files (a
// flat list).
type RecoverySource struct {
	ID               string          `json:"id"`
	Kind             SourceKind      `json:"kind"`
	FSType           FSType          `json:"fsType,omitempty"`
	Offset           int64           `json:"offset"`
	Size             int64           `json:"size,omitempty"` // bytes spanned by the partition/volume
	Label            string          `json:"label,omitempty"`
	Confidence       Confidence      `json:"confidence,omitempty"`
	Root             *Node           `json:"root,omitempty"`  // filesystem sources
	Files            []RecoveredFile `json:"files,omitempty"` // raw sources
	FileCount        int             `json:"fileCount"`
	RecoverableCount int             `json:"recoverableCount"`
}

// ScanRequest is the user's selection of what to scan. Each chosen filesystem
// and the raw source produce an independent RecoverySource in the result.
type ScanRequest struct {
	Filesystems   []FSType `json:"filesystems"`             // e.g. [NTFS, exFAT]
	Raw           bool     `json:"raw"`                     // include a raw carve source
	RawExtensions []string `json:"rawExtensions,omitempty"` // extensions to carve (user-provided, not hardcoded)
	// PartitionOffsets restricts filesystem enumeration to the partitions at
	// these byte offsets (from the partition picker). Empty means scan all.
	PartitionOffsets []int64 `json:"partitionOffsets,omitempty"`
}

// ScanResult holds the separate per-source results, in the order requested.
type ScanResult struct {
	Sources []RecoverySource `json:"sources"`
}
