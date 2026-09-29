package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/manju4682/findit/internal/engines/tsk"
	"github.com/manju4682/findit/internal/jobs"
	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/storage"
	"github.com/manju4682/findit/internal/testutil"
)

func openFixture(t *testing.T, name string) storage.Source {
	return testutil.OpenFixture(t, name)
}

// drain collects every event until the stream closes and returns them.
func drain(s *Scan) []jobs.Event {
	var evs []jobs.Event
	for e := range s.Events() {
		evs = append(evs, e)
	}
	return evs
}

func hasKind(evs []jobs.Event, k jobs.Kind) bool {
	for _, e := range evs {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// badRegionSource wraps a Source and fails reads touching one byte range, like
// a drive with an unreadable sector.
type badRegionSource struct {
	storage.Source
	off, n int64
}

func (b badRegionSource) ReadAt(p []byte, off int64) (int, error) {
	if off < b.off+b.n && off+int64(len(p)) > b.off {
		return 0, errors.New("input/output error")
	}
	return b.Source.ReadAt(p, off)
}

// TestStartScan_SurvivesUnreadableArea proves one bad sector no longer aborts
// the scan: it completes, still carves media elsewhere, and tells the user.
func TestStartScan_SurvivesUnreadableArea(t *testing.T) {
	src := badRegionSource{Source: openFixture(t, "changed_fs_fat32_to_exfat.bin"), off: 100 << 20, n: 512}
	s := StartScan(context.Background(), src, model.ScanRequest{Raw: true, RawExtensions: []string{"jpg"}})
	evs := drain(s)
	if err := s.Wait(); err != nil {
		t.Fatalf("scan failed on an unreadable sector: %v", err)
	}
	warned := false
	for _, e := range evs {
		warned = warned || (e.Kind == jobs.KindLog && strings.Contains(e.Message, "couldn’t be read"))
	}
	if !warned {
		t.Error("expected a log event about the unreadable area")
	}
	if res := s.Result(); len(res.Sources) != 1 || res.Sources[0].FileCount == 0 {
		t.Errorf("expected carved files despite the bad area, got %+v", res.Sources)
	}
}

func TestStartScan_RawSourceOnChangedFilesystem(t *testing.T) {
	src := openFixture(t, "changed_fs_fat32_to_exfat.bin")
	s := StartScan(context.Background(), src, model.ScanRequest{
		Filesystems:   []model.FSType{model.FSExFAT},
		Raw:           true,
		RawExtensions: []string{"jpg", "png", "mp4"},
	})
	evs := drain(s)
	if err := s.Wait(); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if !hasKind(evs, jobs.KindProgress) {
		t.Error("expected a progress event")
	}
	if !hasKind(evs, jobs.KindDone) {
		t.Error("expected a terminal done event")
	}
	if s.Diagnosis() == nil || s.Diagnosis().Current.Type != model.FSExFAT {
		t.Errorf("diagnosis current FS = %v, want exFAT", s.Diagnosis())
	}

	var raw *model.RecoverySource
	for i := range s.Result().Sources {
		if s.Result().Sources[i].Kind == model.SourceRaw {
			raw = &s.Result().Sources[i]
		}
	}
	if raw == nil {
		t.Fatal("expected a raw source in the result")
	}
	if raw.FileCount == 0 {
		t.Error("raw source found no files")
	}
}

func TestStartScan_FilesystemTree(t *testing.T) {
	if !tsk.Available() {
		t.Skip("sleuthkit not on PATH")
	}
	src := openFixture(t, "fat32_files.bin")
	s := StartScan(context.Background(), src, model.ScanRequest{
		Filesystems: []model.FSType{model.FSFAT32},
	})
	drain(s)
	if err := s.Wait(); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	var fs *model.RecoverySource
	for i := range s.Result().Sources {
		if s.Result().Sources[i].Kind == model.SourceFilesystem {
			fs = &s.Result().Sources[i]
		}
	}
	if fs == nil {
		t.Fatal("expected a filesystem source")
	}
	if fs.FSType != model.FSFAT32 || fs.Root == nil {
		t.Fatalf("filesystem source = %+v", fs)
	}
	if !treeHasFile(fs.Root, "IMG_0001.jpg") {
		t.Error("filesystem tree missing IMG_0001.jpg")
	}
}

func TestStartScan_Cancel(t *testing.T) {
	src := openFixture(t, "changed_fs_fat32_to_exfat.bin")
	s := StartScan(context.Background(), src, model.ScanRequest{
		Raw:           true,
		RawExtensions: []string{"jpg", "png", "mp4"},
	})
	s.Cancel()
	// Must terminate promptly without hanging or panicking.
	drain(s)
	_ = s.Wait()
}

func treeHasFile(n *model.Node, name string) bool {
	if n.File != nil && n.Name == name {
		return true
	}
	for _, c := range n.Children {
		if treeHasFile(c, name) {
			return true
		}
	}
	return false
}

func TestPlanTargets_PartitionFilter(t *testing.T) {
	diag := &model.Diagnosis{
		Current: model.FSIdentity{Type: model.FSNTFS, Offset: 1 << 20},
		Present: []model.FSIdentity{
			{Type: model.FSNTFS, Offset: 1 << 20, Size: 100 << 20, Label: "System"},
			{Type: model.FSExFAT, Offset: 200 << 20, Size: 300 << 20, Label: "Data"},
		},
	}
	want := []model.FSType{model.FSNTFS, model.FSExFAT}

	// No offsets => every present partition is a target.
	all := planTargets(diag, want, nil)
	if len(all) != 2 {
		t.Fatalf("scan-all: got %d targets, want 2: %+v", len(all), all)
	}

	// Restrict to the exFAT partition's offset only.
	only := planTargets(diag, want, []int64{200 << 20})
	if len(only) != 1 || only[0].fsType != model.FSExFAT || only[0].offset != 200<<20 {
		t.Fatalf("filtered: got %+v, want just exFAT@200MiB", only)
	}
	if only[0].size != 300<<20 || only[0].label != "Data" {
		t.Errorf("target lost size/label: %+v", only[0])
	}
}
