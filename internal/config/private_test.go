package config

import (
	"errors"
	"testing"
)

// TestRecordScheduled checks that scheduled runs are remembered separately from
// ordinary syncs, that a later run replaces an earlier one (a success clears an
// old failure and vice versa), and that the record survives a restart.
func TestRecordScheduled(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	st := LoadState()

	st.Update("hist", func(p *PlaylistState) { p.Summary = "manual sync" })
	if got := st.Get("hist"); !got.LastScheduled.IsZero() {
		t.Fatalf("a manual sync must not count as a scheduled run: %+v", got)
	}

	st.RecordScheduled("hist", "updated · 25 items", errors.New("boom"))
	got := st.Get("hist")
	if got.LastScheduled.IsZero() || got.ScheduledError != "boom" || got.ScheduledSummary != "" {
		t.Fatalf("failure not recorded: %+v", got)
	}

	st.RecordScheduled("hist", "unchanged · 25 items", nil)
	got = LoadState().Get("hist") // read back from disk
	if got.ScheduledError != "" || got.ScheduledSummary != "unchanged · 25 items" || got.Summary != "manual sync" {
		t.Fatalf("success should replace the failure and leave the manual result alone: %+v", got)
	}
}
