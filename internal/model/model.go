// Package model defines the core, engine-agnostic data types that flow between
// FindIt's diagnosis, recovery engines, merger, and UI. Nothing in this package
// depends on a specific recovery engine or operating system.
package model

// FSType identifies a filesystem kind.
type FSType string

const (
	FSUnknown FSType = "unknown"
	FSNTFS    FSType = "NTFS"
	FSFAT32   FSType = "FAT32"
	FSExFAT   FSType = "exFAT"
)

// Confidence is a coarse, user-facing bucket derived from an evidence score.
type Confidence string

const (
	ConfHigh   Confidence = "High"
	ConfMedium Confidence = "Medium"
	ConfLow    Confidence = "Low"
)

// Evidence is a single, located observation that supports a conclusion. It is
// what the Advanced Mode panel renders verbatim.
type Evidence struct {
	Kind   string `json:"kind"` // "boot-sector" | "fs-type-string" | "mft-record" | "volume-label"
	FSType FSType `json:"fsType"`
	Offset int64  `json:"offset"` // byte offset within the image
	Detail string `json:"detail,omitempty"`
}

// FSIdentity describes the filesystem the drive currently presents.
type FSIdentity struct {
	Type   FSType `json:"type"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size,omitempty"` // bytes spanned by the partition/volume
	Label  string `json:"label,omitempty"`
}

// FilesystemCandidate is a previous/underlying filesystem discovered beneath the
// current one — the heart of the "your old files are still here" diagnosis.
type FilesystemCandidate struct {
	Type       FSType     `json:"type"`
	Offset     int64      `json:"offset"`
	Label      string     `json:"label,omitempty"`
	Score      int        `json:"score"` // 0..100
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// CarveSummary counts raw file signatures found by content scanning, regardless
// of any filesystem. Keys are lowercase extensions ("jpg","png","mp4").
type CarveSummary struct {
	Counts map[string]int `json:"counts"`
}

// Photos returns the number of still-image signatures found.
func (c CarveSummary) Photos() int { return c.Counts["jpg"] + c.Counts["png"] }

// Videos returns the number of video signatures found.
func (c CarveSummary) Videos() int { return c.Counts["mp4"] }

// Diagnosis is the complete output of the Diagnosis Engine for one image.
type Diagnosis struct {
	ImagePath  string                `json:"imagePath"`
	ImageSize  int64                 `json:"imageSize"`
	Current    FSIdentity            `json:"current"`
	Present    []FSIdentity          `json:"present,omitempty"` // filesystems found in partitions
	Candidates []FilesystemCandidate `json:"candidates"`
	Carve      CarveSummary          `json:"carve"`
	Narrative  string                `json:"narrative"`
}
