// Package session orchestrates a full scan: it runs diagnosis, then produces one
// independent RecoverySource per selected target (filesystems via TSK, raw via
// the carver). Results are kept separate and stream over a jobs.Job.
package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/findit/findit/internal/carve"
	"github.com/findit/findit/internal/config"
	"github.com/findit/findit/internal/diagnosis"
	"github.com/findit/findit/internal/engines/tsk"
	"github.com/findit/findit/internal/jobs"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
)

// Scan is a running recovery scan: a Job plus the diagnosis and per-source
// results accumulated so far.
type Scan struct {
	*jobs.Job

	mu      sync.Mutex
	diag    *model.Diagnosis
	sources []model.RecoverySource
}

// Diagnosis returns the diagnosis once available (nil before it completes).
func (s *Scan) Diagnosis() *model.Diagnosis {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diag
}

// Result returns a snapshot of the per-source results gathered so far.
func (s *Scan) Result() model.ScanResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.RecoverySource, len(s.sources))
	copy(out, s.sources)
	return model.ScanResult{Sources: out}
}

func (s *Scan) setDiag(d *model.Diagnosis) {
	s.mu.Lock()
	s.diag = d
	s.mu.Unlock()
}

func (s *Scan) addSource(rs model.RecoverySource) {
	s.mu.Lock()
	s.sources = append(s.sources, rs)
	s.mu.Unlock()
}

// StartScan begins a scan and returns immediately. Consumers drain Events()
// until closed, then read Result().
func StartScan(parent context.Context, src storage.Source, req model.ScanRequest) *Scan {
	s := &Scan{}
	s.Job = jobs.Run(parent, config.Get().Scan.EventBufferSize, func(ctx context.Context, emit func(jobs.Event)) error {
		emit(jobs.Event{Kind: jobs.KindProgress, Phase: "diagnose", Message: "Analyzing the drive…"})
		diag, err := diagnosis.DiagnoseSource(src)
		if err != nil {
			return fmt.Errorf("diagnosis failed: %w", err)
		}
		s.setDiag(diag)
		emit(jobs.Event{Kind: jobs.KindProgress, Phase: "diagnose", Message: diag.Narrative})

		if err := s.enumerateFilesystems(ctx, src, diag, req, emit); err != nil {
			return err
		}
		if req.Raw && len(req.RawExtensions) > 0 {
			if err := s.carveRaw(ctx, src, req, emit); err != nil {
				return err
			}
		}
		return nil
	})
	return s
}

func (s *Scan) enumerateFilesystems(ctx context.Context, src storage.Source, diag *model.Diagnosis, req model.ScanRequest, emit func(jobs.Event)) error {
	for _, tgt := range planTargets(diag, req.Filesystems, req.PartitionOffsets) {
		if err := ctx.Err(); err != nil {
			return err
		}
		emit(jobs.Event{Kind: jobs.KindSourceStarted, Phase: "enumerate",
			Message: fmt.Sprintf("Reading the %s filesystem…", tgt.fsType)})

		if !tsk.Available() {
			emit(jobs.Event{Kind: jobs.KindLog, Phase: "enumerate",
				Message: "The Sleuth Kit is not installed; skipping filesystem enumeration."})
			continue
		}
		eng := tsk.New(src.Name(), tgt.offset, tgt.fsType)
		fsSrc, err := eng.Enumerate(ctx)
		if err != nil {
			emit(jobs.Event{Kind: jobs.KindLog, Phase: "enumerate",
				Message: fmt.Sprintf("Couldn't read the %s structure; raw recovery may still find files.", tgt.fsType)})
			continue
		}
		fsSrc.Confidence = tgt.conf
		fsSrc.Size = tgt.size
		if tgt.label != "" {
			fsSrc.Label = tgt.label
		}
		s.addSource(*fsSrc)
		emit(jobs.Event{Kind: jobs.KindSourceDone, Phase: "enumerate", Source: fsSrc,
			Message: fmt.Sprintf("Found %d file(s) in the %s filesystem.", fsSrc.FileCount, tgt.fsType)})
	}
	return nil
}

func (s *Scan) carveRaw(ctx context.Context, src storage.Source, req model.ScanRequest, emit func(jobs.Event)) error {
	emit(jobs.Event{Kind: jobs.KindSourceStarted, Phase: "carve", Message: "Scanning for files by content…"})
	carved, err := carve.Scan(ctx, src, req.RawExtensions)
	if err != nil {
		return fmt.Errorf("raw scan failed: %w", err)
	}
	raw := buildRawSource(carved)
	s.addSource(raw)
	emit(jobs.Event{Kind: jobs.KindSourceDone, Phase: "carve", Source: &raw,
		Message: fmt.Sprintf("Found %d file(s) by content.", raw.FileCount)})
	return nil
}

// target is a filesystem to enumerate at a specific offset.
type target struct {
	fsType model.FSType
	offset int64
	size   int64
	conf   model.Confidence
	label  string
}

// planTargets maps the requested filesystem types to concrete offsets using the
// diagnosis: the current filesystem (offset 0) and any matching previous-FS
// candidates. When offsets is non-empty, present partitions are restricted to
// those byte offsets (the partition picker); candidates underneath are always
// kept, since surfacing older files is the whole point.
func planTargets(diag *model.Diagnosis, want []model.FSType, offsets []int64) []target {
	wanted := map[model.FSType]bool{}
	for _, t := range want {
		wanted[t] = true
	}
	chosen := map[int64]bool{}
	for _, o := range offsets {
		chosen[o] = true
	}
	partitionWanted := func(off int64) bool { return len(chosen) == 0 || chosen[off] }

	var targets []target
	seen := map[string]bool{}
	add := func(t target) {
		key := fmt.Sprintf("%s@%d", t.fsType, t.offset)
		if !seen[key] {
			seen[key] = true
			targets = append(targets, t)
		}
	}

	// Filesystems present in partitions, at their real offsets.
	for _, p := range diag.Present {
		if wanted[p.Type] && partitionWanted(p.Offset) {
			add(target{fsType: p.Type, offset: p.Offset, size: p.Size, label: p.Label})
		}
	}
	if wanted[diag.Current.Type] && partitionWanted(diag.Current.Offset) {
		add(target{fsType: diag.Current.Type, offset: diag.Current.Offset, size: diag.Current.Size, label: diag.Current.Label})
	}
	for _, c := range diag.Candidates {
		if wanted[c.Type] {
			add(target{fsType: c.Type, offset: c.Offset, conf: c.Confidence, label: c.Label})
		}
	}
	return targets
}

// buildRawSource converts carved files into a raw RecoverySource (a flat list).
// Content-quality scores (0..100) come from config: a structurally valid carve
// is treated as complete; an invalid one is a low-confidence fragment.
func buildRawSource(carved []carve.Carved) model.RecoverySource {
	sc := config.Get().Scan
	files := make([]model.RecoveredFile, 0, len(carved))
	for i, c := range carved {
		status := model.StatusGood
		if !c.Valid {
			status = model.StatusRawFragment
		}
		content := sc.RawContentComplete
		if !c.Valid {
			content = sc.RawContentFragment
		}
		files = append(files, model.RecoveredFile{
			ID:          fmt.Sprintf("raw-%d", i),
			SourceID:    "raw",
			Name:        fmt.Sprintf("%s_%08x.%s", c.Ext, c.Offset, c.Ext),
			Ext:         c.Ext,
			Size:        c.Length,
			Extent:      []model.ByteRange{{Offset: c.Offset, Length: c.Length}},
			Previewable: previewable(c.Ext),
			Recoverable: c.Valid,
			Assessment:  model.RecoveryAssessment{Content: content, Status: status},
		})
	}
	recoverable := 0
	for _, f := range files {
		if f.Recoverable {
			recoverable++
		}
	}
	return model.RecoverySource{
		ID:               "raw",
		Kind:             model.SourceRaw,
		Files:            files,
		FileCount:        len(files),
		RecoverableCount: recoverable,
	}
}

func previewable(ext string) bool {
	switch ext {
	case "jpg", "jpeg", "png", "gif", "mp4", "mov", "m4v":
		return true
	}
	return false
}
