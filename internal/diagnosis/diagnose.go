package diagnosis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/findit/findit/internal/config"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
)

// Diagnose opens a disk image and produces a full Diagnosis. It is a thin
// convenience wrapper over DiagnoseSource.
func Diagnose(path string) (*model.Diagnosis, error) {
	src, err := storage.OpenImage(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return DiagnoseSource(src)
}

// DiagnoseSource produces a full Diagnosis from any read-only Source: the
// current filesystem, previous/underlying filesystem candidates, a raw media
// summary, and a plain-English narrative.
func DiagnoseSource(src storage.Source) (*model.Diagnosis, error) {
	// Use the source's real sector size (512 for images, the device's actual
	// block size otherwise) so partition offsets are correct on 4Kn drives too.
	bs := sectorSizeOf(src)

	head := make([]byte, bs)
	if _, err := src.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("reading boot sector: %w", err)
	}
	curType, curLabel := identifyBoot(head)
	current := model.FSIdentity{Type: curType, Offset: 0, Size: src.Size(), Label: curLabel}

	// Only look for partitions when offset 0 is not itself a filesystem — a real
	// FS boot sector also ends in 0x55AA and would be misread as an MBR.
	var present []model.FSIdentity
	if current.Type == model.FSUnknown {
		present = partitionFilesystems(src, bs)
		if len(present) > 0 {
			current = present[0]
		}
	}

	presentTypes := map[model.FSType]bool{current.Type: true}
	for _, p := range present {
		presentTypes[p.Type] = true
	}

	res, err := scan(storage.Reader(src), current.Type)
	if err != nil {
		return nil, err
	}

	carve := model.CarveSummary{Counts: res.carve}
	d := &model.Diagnosis{
		ImagePath:  src.Name(),
		ImageSize:  src.Size(),
		Current:    current,
		Present:    present,
		Candidates: buildCandidates(res, presentTypes, carve),
		Carve:      carve,
	}
	d.Narrative = narrate(d)
	return d, nil
}

// Partitions returns the filesystem-bearing partitions on the source without a
// full content scan — fast enough to drive an interactive partition picker.
// When the drive holds a single whole-disk filesystem (no partition table), it
// returns that one filesystem at offset 0.
func Partitions(src storage.Source) []model.FSIdentity {
	bs := sectorSizeOf(src)
	head := make([]byte, bs)
	if _, err := src.ReadAt(head, 0); err != nil {
		return nil
	}
	if t, label := identifyBoot(head); t != model.FSUnknown {
		return []model.FSIdentity{{Type: t, Offset: 0, Size: src.Size(), Label: label}}
	}
	return partitionFilesystems(src, bs)
}

// partitionFilesystems identifies the filesystem in each partition of a
// partitioned drive, at its real byte offset.
func partitionFilesystems(src storage.Source, bs int) []model.FSIdentity {
	var present []model.FSIdentity
	sec := make([]byte, bs)
	for _, p := range parsePartitions(src, int64(bs)) {
		if _, err := src.ReadAt(sec, p.offset); err != nil {
			continue
		}
		if t, label := identifyBoot(sec); t != model.FSUnknown {
			present = append(present, model.FSIdentity{Type: t, Offset: p.offset, Size: p.size, Label: label})
		}
	}
	return present
}

// sectorSizeOf returns the source's logical sector size, falling back to the
// 512-byte default when the source can't report one.
func sectorSizeOf(src storage.Source) int {
	if bs := src.SectorSize(); bs > 0 {
		return bs
	}
	return sectorSize
}

// buildCandidates converts raw evidence into scored, deduplicated previous /
// underlying filesystem candidates, excluding filesystems already present in a
// partition.
func buildCandidates(res *scanResult, presentTypes map[model.FSType]bool, carve model.CarveSummary) []model.FilesystemCandidate {
	byType := map[model.FSType]*model.FilesystemCandidate{}
	get := func(t model.FSType) *model.FilesystemCandidate {
		if c := byType[t]; c != nil {
			return c
		}
		c := &model.FilesystemCandidate{Type: t}
		byType[t] = c
		return c
	}

	for _, e := range res.boot {
		if presentTypes[e.FSType] {
			continue
		}
		c := get(e.FSType)
		c.Evidence = append(c.Evidence, e)
		if c.Offset == 0 {
			c.Offset = e.Offset
		}
		if c.Label == "" && e.Detail != "" {
			c.Label = e.Detail
		}
	}
	for t, offs := range res.typeHits {
		if presentTypes[t] || len(offs) == 0 {
			continue
		}
		c := get(t)
		c.Evidence = append(c.Evidence, model.Evidence{
			Kind: "fs-type-string", FSType: t, Offset: offs[0],
			Detail: fmt.Sprintf("%d occurrence(s)", len(offs)),
		})
	}
	if len(res.mftHits) > 0 && !presentTypes[model.FSNTFS] {
		c := get(model.FSNTFS)
		c.Evidence = append(c.Evidence, model.Evidence{
			Kind: "mft-record", FSType: model.FSNTFS, Offset: res.mftHits[0],
			Detail: fmt.Sprintf("%d MFT FILE record(s)", len(res.mftHits)),
		})
	}

	mediaPresent := carve.Photos()+carve.Videos() > 0
	var out []model.FilesystemCandidate
	for t, c := range byType {
		if presentTypes[t] || t == model.FSUnknown {
			continue
		}
		c.Score, c.Confidence = score(c, mediaPresent)
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// scoreMax is the top of the 0..100 evidence scale (a clamp ceiling, not a knob).
const scoreMax = 100

// score weights a candidate's evidence into a 0..100 value and a bucket, using
// the tunable weights and cutoffs from the config package.
func score(c *model.FilesystemCandidate, mediaPresent bool) (int, model.Confidence) {
	d := config.Get().Diagnosis
	var s int
	var hasBoot, hasString, hasMFT bool
	for _, e := range c.Evidence {
		switch e.Kind {
		case "boot-sector":
			hasBoot = true
		case "fs-type-string":
			hasString = true
		case "mft-record":
			hasMFT = true
		}
	}
	if hasBoot {
		s += d.ScoreBootSector
	}
	if hasString {
		s += d.ScoreTypeString
	}
	if hasMFT {
		s += d.ScoreMFTRecord
	}
	if mediaPresent {
		s += d.ScoreMediaBonus
	}
	if s > scoreMax {
		s = scoreMax
	}
	switch {
	case s >= d.ConfHighCutoff:
		return s, model.ConfHigh
	case s >= d.ConfMediumCutoff:
		return s, model.ConfMedium
	default:
		return s, model.ConfLow
	}
}

// narrate turns a Diagnosis into user-facing language (no jargon).
func narrate(d *model.Diagnosis) string {
	var b strings.Builder

	switch {
	case len(d.Present) > 0:
		fmt.Fprintf(&b, "This drive has a %s filesystem. ", d.Present[0].Type)
	case d.Current.Type != model.FSUnknown:
		fmt.Fprintf(&b, "This drive has a %s filesystem. ", d.Current.Type)
	default:
		b.WriteString("This drive's filesystem couldn't be read directly. ")
	}

	if len(d.Candidates) > 0 {
		top := d.Candidates[0]
		fmt.Fprintf(&b,
			"We also found signs of an earlier %s filesystem underneath — your older files may still be recoverable. ",
			top.Type)
	}

	photos, videos := d.Carve.Photos(), d.Carve.Videos()
	if photos+videos > 0 {
		fmt.Fprintf(&b, "We can also see about %d photo(s) and %d video(s) by content.", photos, videos)
	}
	return strings.TrimSpace(b.String())
}
