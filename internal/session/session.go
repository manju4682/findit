// Package session orchestrates a full scan: it runs diagnosis, then produces one
// independent RecoverySource per selected target (filesystems via TSK, raw via
// the carver). Results are kept separate and stream over a jobs.Job.
package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/findit/findit/internal/carve"
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
	s.Job = jobs.Run(parent, 128, func(ctx context.Context, emit func(jobs.Event)) error {
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
	for _, tgt := range planTargets(diag, req.Filesystems) {
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
	conf   model.Confidence
	label  string
}

// planTargets maps the requested filesystem types to concrete offsets using the
// diagnosis: the current filesystem (offset 0) and any matching previous-FS
// candidates.
func planTargets(diag *model.Diagnosis, want []model.FSType) []target {
	wanted := map[model.FSType]bool{}
	for _, t := range want {
		wanted[t] = true
	}
	var targets []target
	seen := map[string]bool{}
	add := func(t target) {
		key := fmt.Sprintf("%s@%d", t.fsType, t.offset)
		if !seen[key] {
			seen[key] = true
			targets = append(targets, t)
		}
	}

	if wanted[diag.Current.Type] {
		add(target{fsType: diag.Current.Type, offset: 0, label: diag.Current.Label})
	}
	for _, c := range diag.Candidates {
		if wanted[c.Type] {
			add(target{fsType: c.Type, offset: c.Offset, conf: c.Confidence, label: c.Label})
		}
	}
	return targets
}

// buildRawSource converts carved files into a raw RecoverySource (a flat list).
func buildRawSource(carved []carve.Carved) model.RecoverySource {
	files := make([]model.RecoveredFile, 0, len(carved))
	for i, c := range carved {
		status := model.StatusGood
		if !c.Valid {
			status = model.StatusRawFragment
		}
		content := 100
		if !c.Valid {
			content = 40
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
	return model.RecoverySource{
		ID:        "raw",
		Kind:      model.SourceRaw,
		Files:     files,
		FileCount: len(files),
	}
}

func previewable(ext string) bool {
	switch ext {
	case "jpg", "jpeg", "png", "gif", "mp4", "mov", "m4v":
		return true
	}
	return false
}
