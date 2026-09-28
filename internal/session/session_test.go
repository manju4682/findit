package session

import (
	"context"
	"testing"

	"github.com/findit/findit/internal/engines/tsk"
	"github.com/findit/findit/internal/jobs"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/storage"
	"github.com/findit/findit/internal/testutil"
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
