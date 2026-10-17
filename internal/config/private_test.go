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

// TestStateSharedBetweenProcesses simulates the TUI and a cron run, each with
// its own State loaded from the same file. A write by one must not erase what
// the other recorded in between (the "lost update" the old whole-file overwrite
// allowed), and Reload must make the other's changes visible.
func TestStateSharedBetweenProcesses(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	tui := LoadState()  // the long-running TUI, loaded before cron runs
	cron := LoadState() // a separate `abp sync -scheduled` process

	cron.RecordScheduled("hist", "updated · 5 items", nil)

	// The TUI doesn't know about that yet...
	if got := tui.Get("hist"); !got.LastScheduled.IsZero() {
		t.Fatalf("TUI should not see the cron run before reloading: %+v", got)
	}
	// ...and does a manual sync, which writes the file. It must keep cron's record.
	tui.Update("hist", func(p *PlaylistState) { p.Summary = "manual sync" })
	got := LoadState().Get("hist")
	if got.ScheduledSummary != "updated · 5 items" || got.Summary != "manual sync" {
		t.Fatalf("one process clobbered the other's write: %+v", got)
	}

	// Reload picks up changes made elsewhere.
	cron.RecordScheduled("other", "", errors.New("boom"))
	tui.Reload()
	if got := tui.Get("other"); got.ScheduledError != "boom" {
		t.Fatalf("Reload did not pick up the other process's write: %+v", got)
	}

	// Forget must not resurrect or drop other playlists' records either.
	tui.Forget("hist")
	if after := LoadState(); after.Get("other").ScheduledError != "boom" || after.Get("hist").Summary != "" {
		t.Fatalf("Forget misbehaved: %+v", after.Playlists)
	}
}

// TestStateReloadKeepsMemoryWhenFileMissing makes sure a missing file doesn't
// wipe what is in memory (the first sync of a fresh install, for example).
func TestStateReloadKeepsMemoryWhenFileMissing(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	s := &State{Playlists: map[string]*PlaylistState{"x": {Summary: "kept"}}}
	s.Reload()
	if s.Get("x").Summary != "kept" {
		t.Fatal("reload with no file on disk must keep the in-memory state")
	}
}
