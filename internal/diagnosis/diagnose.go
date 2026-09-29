package diagnosis

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/manju4682/findit/internal/bytescan"
	"github.com/manju4682/findit/internal/config"
	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/storage"
)

// Diagnose opens a disk image and produces a full Diagnosis. It is a thin
// convenience wrapper over DiagnoseSource.
func Diagnose(path string) (*model.Diagnosis, error) {
	src, err := storage.OpenImage(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return DiagnoseSource(context.Background(), src, nil)
}

// DiagnoseSource produces a full Diagnosis from any read-only Source in one
// streaming pass. It stops promptly if ctx is canceled, and reports scan
// progress through progress (done, total bytes) when non-nil. Callers that also
// carve can share the pass via Prepare/Scanner/Build instead.
func DiagnoseSource(ctx context.Context, src storage.Source, progress func(done, total int64)) (*model.Diagnosis, error) {
	prep, err := Prepare(src)
	if err != nil {
		return nil, err
	}
	sc := prep.NewScanner()
	total := src.Size()
	err = bytescan.Scan(ctx, storage.Reader(src), sc.Overlap(), func(win []byte, base int64, safe int) {
		sc.Visit(win, base, safe)
		if progress != nil {
			progress(base+int64(safe), total)
		}
	})
	if err != nil {
		return nil, err
	}
	return prep.Build(sc), nil
}

// Prep holds the fast pre-scan facts about a source — the current filesystem and
// any underlying/partition filesystems — gathered from small reads before the
// full content pass. It is the input to a single pass that can be shared with
// carving.
type Prep struct {
	src          storage.Source
	current      model.FSIdentity
	present      []model.FSIdentity
	presentTypes map[model.FSType]bool
}

// Prepare reads the boot sector and partition table (small reads only) to learn
// the current and underlying filesystems, without the full content pass.
func Prepare(src storage.Source) (*Prep, error) {
	// Use the source's real sector size (512 for images, the device's actual
	// block size otherwise) so partition offsets are correct on 4Kn drives too.
	bs := sectorSizeOf(src)
	head := make([]byte, bs)
	if _, err := src.ReadAt(head, 0); err != nil {
		clear(head) // unreadable first sector: no current filesystem, raw recovery still runs
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
	return &Prep{src: src, current: current, present: present, presentTypes: presentTypes}, nil
}

// Scanner accumulates content evidence (boot sectors, MFT records, type strings,
// media counts) from streamed windows. Feed every window to Visit, then Build.
type Scanner struct {
	res     *scanResult
	curType model.FSType
}

// NewScanner returns a content Scanner for this Prep.
func (p *Prep) NewScanner() *Scanner {
	return &Scanner{res: newScanResult(), curType: p.current.Type}
}

// Overlap is the trailing overlap the scanner needs between windows (it must
// exceed one sector so the aligned boot-sector probe can read a full sector).
func (s *Scanner) Overlap() int { return config.Get().Diagnosis.ScanOverlapBytes }

// Visit processes one streamed window.
func (s *Scanner) Visit(win []byte, base int64, safe int) {
	scanWindow(s.res, win, safe, base, s.curType)
}

// Build assembles the final Diagnosis from a completed Scanner.
func (p *Prep) Build(s *Scanner) *model.Diagnosis {
	carve := model.CarveSummary{Counts: s.res.carve}
	d := &model.Diagnosis{
		ImagePath:  p.src.Name(),
		ImageSize:  p.src.Size(),
		Current:    p.current,
		Present:    p.present,
		Candidates: buildCandidates(s.res, p.presentTypes, carve),
		Carve:      carve,
	}
	d.Narrative = narrate(d)
	return d
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
	for t, h := range res.typeHits {
		if presentTypes[t] || h.count == 0 {
			continue
		}
		c := get(t)
		c.Evidence = append(c.Evidence, model.Evidence{
			Kind: "fs-type-string", FSType: t, Offset: h.first,
			Detail: fmt.Sprintf("%d occurrence(s)", h.count),
		})
	}
	if res.mftHits.count > 0 && !presentTypes[model.FSNTFS] {
		c := get(model.FSNTFS)
		c.Evidence = append(c.Evidence, model.Evidence{
			Kind: "mft-record", FSType: model.FSNTFS, Offset: res.mftHits.first,
			Detail: fmt.Sprintf("%d MFT FILE record(s)", res.mftHits.count),
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
		fmt.Fprintf(&b, "This drive has %s %s filesystem. ", article(d.Present[0].Type), d.Present[0].Type)
	case d.Current.Type != model.FSUnknown:
		fmt.Fprintf(&b, "This drive has %s %s filesystem. ", article(d.Current.Type), d.Current.Type)
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

// article picks "a" or "an" by how the name is spoken ("an exFAT", "an NTFS", "a FAT32").
func article(t model.FSType) string {
	switch t {
	case model.FSExFAT, model.FSNTFS:
		return "an"
	}
	return "a"
}
