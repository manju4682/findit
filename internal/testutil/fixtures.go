// Package testutil provides shared helpers for tests that read disk-image
// fixtures from testdata/fixtures.
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manju4682/findit/internal/storage"
)

// FixturePath returns the path to a named fixture, skipping the test if it is
// absent (fixtures are generated locally and not committed).
func FixturePath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "testdata", "fixtures", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture %s not present (%v); generate it with the scripts in scripts/", name, err)
	}
	return p
}

// OpenFixture opens a named fixture image read-only and closes it on cleanup.
func OpenFixture(t *testing.T, name string) storage.Source {
	t.Helper()
	src, err := storage.OpenImage(FixturePath(t, name))
	if err != nil {
		t.Fatalf("OpenImage: %v", err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// repoRoot walks up from the working directory to the module root (go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("testutil: go.mod not found above working directory")
		}
		dir = parent
	}
}
