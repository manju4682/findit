package diagnosis

import (
	"fmt"
	"sort"
	"strings"

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
	head := make([]byte, sectorSize)
	if _, err := src.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("reading boot sector: %w", err)
	}
	curType, curLabel := identifyBoot(head)

	res, err := scan(storage.Reader(src), curType)
	if err != nil {
		return nil, err
	}

	carve := model.CarveSummary{Counts: res.carve}
	d := &model.Diagnosis{
		ImagePath:  src.Name(),
		ImageSize:  src.Size(),
		Current:    model.FSIdentity{Type: curType, Offset: 0, Label: curLabel},
		Candidates: buildCandidates(res, curType, carve),
		Carve:      carve,
	}
	d.Narrative = narrate(d)
	return d, nil
}

// buildCandidates converts raw evidence into scored, deduplicated filesystem
// candidates, excluding the current filesystem.
func buildCandidates(res *scanResult, curType model.FSType, carve model.CarveSummary) []model.FilesystemCandidate {
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
		if t == curType || len(offs) == 0 {
			continue
		}
		c := get(t)
		c.Evidence = append(c.Evidence, model.Evidence{
			Kind: "fs-type-string", FSType: t, Offset: offs[0],
			Detail: fmt.Sprintf("%d occurrence(s)", len(offs)),
		})
	}
	if len(res.mftHits) > 0 {
		c := get(model.FSNTFS)
		c.Evidence = append(c.Evidence, model.Evidence{
			Kind: "mft-record", FSType: model.FSNTFS, Offset: res.mftHits[0],
			Detail: fmt.Sprintf("%d MFT FILE record(s)", len(res.mftHits)),
		})
	}

	mediaPresent := carve.Photos()+carve.Videos() > 0
	var out []model.FilesystemCandidate
	for t, c := range byType {
		if t == curType || t == model.FSUnknown {
			continue
		}
		c.Score, c.Confidence = score(c, mediaPresent)
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// score weights a candidate's evidence into a 0..100 value and a bucket.
func score(c *model.FilesystemCandidate, mediaPresent bool) (int, model.Confidence) {
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
		s += 40
	}
	if hasString {
		s += 25
	}
	if hasMFT {
		s += 20
	}
	if mediaPresent {
		s += 15
	}
	if s > 100 {
		s = 100
	}
	switch {
	case s >= 75:
		return s, model.ConfHigh
	case s >= 40:
		return s, model.ConfMedium
	default:
		return s, model.ConfLow
	}
}

// narrate turns a Diagnosis into user-facing language (no jargon).
func narrate(d *model.Diagnosis) string {
	var b strings.Builder
	cur := string(d.Current.Type)
	if d.Current.Type == model.FSUnknown {
		cur = "an unrecognized format"
	}
	fmt.Fprintf(&b, "This drive currently appears as %s. ", cur)

	if len(d.Candidates) > 0 {
		top := d.Candidates[0]
		fmt.Fprintf(&b,
			"We found signs of an earlier %s filesystem underneath it (confidence: %s). "+
				"Your previous files appear to still be present. ",
			top.Type, strings.ToLower(string(top.Confidence)))
	} else {
		b.WriteString("We did not detect an earlier filesystem, but we can still look for files by their content. ")
	}

	photos, videos := d.Carve.Photos(), d.Carve.Videos()
	if photos+videos > 0 {
		fmt.Fprintf(&b, "So far we can see about %d photo(s) and %d video(s) that may be recoverable.", photos, videos)
	} else {
		b.WriteString("No recoverable photos or videos have been found yet.")
	}
	return b.String()
}
