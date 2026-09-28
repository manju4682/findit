package config

import "testing"

// TestGet_MatchesTemplate locks the embedded template and the code fallback in
// sync: if someone edits templates/configmap.yml, these assertions catch drift.
func TestGet_MatchesTemplate(t *testing.T) {
	got := Get()
	want := fallback()
	if got != want {
		t.Fatalf("Get() = %+v, want %+v (embedded template drifted from fallback)", got, want)
	}
}

func TestClone_PollInterval(t *testing.T) {
	c := Clone{PollIntervalMS: 500}
	if got := c.PollInterval().Milliseconds(); got != 500 {
		t.Fatalf("PollInterval() = %dms, want 500ms", got)
	}
}
