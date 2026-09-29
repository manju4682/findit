package diagnosis

import (
	"testing"

	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/testutil"
)

func TestDiagnose_ChangedFilesystem(t *testing.T) {
	d, err := Diagnose(testutil.FixturePath(t, "changed_fs_fat32_to_exfat.bin"))
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	if d.Current.Type != model.FSExFAT {
		t.Errorf("current FS = %q, want exFAT", d.Current.Type)
	}

	var fat *model.FilesystemCandidate
	for i := range d.Candidates {
		if d.Candidates[i].Type == model.FSFAT32 {
			fat = &d.Candidates[i]
			break
		}
	}
	if fat == nil {
		t.Fatalf("expected a FAT32 previous-filesystem candidate; got %+v", d.Candidates)
	}
	if len(fat.Evidence) == 0 {
		t.Errorf("FAT32 candidate has no evidence")
	}

	// exFAT is the current FS and must never be reported as a previous candidate.
	for _, c := range d.Candidates {
		if c.Type == model.FSExFAT {
			t.Errorf("current exFAT must not appear as a previous candidate")
		}
	}

	if got := d.Carve.Photos(); got < 8 {
		t.Errorf("photos found = %d, want >= 8", got)
	}
	if got := d.Carve.Videos(); got < 3 {
		t.Errorf("videos found = %d, want >= 3", got)
	}
	if d.Narrative == "" {
		t.Error("narrative is empty")
	}
}
