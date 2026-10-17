package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/projector22/abp/internal/abp"
	"github.com/projector22/abp/internal/config"
	"github.com/projector22/abp/internal/version"
)

// TestRenderAllScreens is a smoke test: it draws every screen at a normal and at
// a tiny terminal size and fails on a panic or an empty frame. It catches layout
// arithmetic mistakes (negative widths, empty lists) that the compiler cannot.
func TestRenderAllScreens(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	t.Setenv("ABP_CONFIG", t.TempDir()+"/config.yaml")
	a := &app{
		cfg: &config.Config{Playlists: []config.Playlist{{
			ID: "hist", Name: "History Backlog", Library: "l1", LibraryName: "Podcasts", Limit: 25,
			Schedule: "*/30 * * * *", Podcasts: []config.Podcast{{ID: "p1", Title: "Revolutions"}, {ID: "p2", Title: "Fall of Civilizations"}},
		}}},
		state: config.LoadState(), podcasts: map[string][]abp.LibraryItem{},
		libs:    []abp.Library{{ID: "l1", Name: "Podcasts", MediaType: "podcast"}},
		managed: map[string]string{},
	}
	a.creds = config.Credentials{URL: "http://abs.local:13378", APIKey: "k"}
	a.podcasts["l1"] = []abp.LibraryItem{{ID: "p1", Title: "Revolutions", Author: "Mike"}, {ID: "p3", Title: "Hardcore History", Author: "Dan"}}

	r := newRoot(a, newList(a))
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_ = r.View()
	for _, s := range []screen{newDetail(a, "hist"), newPicker(a, "hist"), newForm(a, "", false), newForm(a, "hist", true), newPreview(a, "hist"), newSetup(a, true)} {
		s.Init()
		r.Update(pushMsg{s})
		r.Update(tea.KeyMsg{Type: tea.KeyDown})
		out := r.View()
		if out == "" {
			t.Fatal("empty view")
		}

	}
	// tiny terminal must not panic
	r.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	r.View()
}

// TestPickerTogglesAndPersistsImmediately checks the picker contract: each
// toggle changes the playlist and is already on disk in config.yaml, both when
// adding and when removing, without leaving the screen.
func TestPickerTogglesAndPersistsImmediately(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	t.Setenv("ABP_CONFIG", t.TempDir()+"/config.yaml")
	a := &app{
		cfg: &config.Config{Playlists: []config.Playlist{{
			ID: "hist", Name: "H", Library: "l1", Limit: 5, Podcasts: []config.Podcast{},
		}}},
		state: config.LoadState(), managed: map[string]string{},
		podcasts: map[string][]abp.LibraryItem{"l1": {
			{ID: "p1", Title: "Alpha"}, {ID: "p2", Title: "Beta"},
		}},
	}
	p := newPicker(a, "hist")
	p.Init()
	key := func(t tea.KeyType) { p.Update(tea.KeyMsg{Type: t}) }

	key(tea.KeyTab) // add Alpha
	if !a.cfg.Playlists[0].HasPodcast("p1") {
		t.Fatal("Alpha should be added after one toggle")
	}
	onDisk, _ := config.Load()
	if len(onDisk.Playlists) != 1 || !onDisk.Playlists[0].HasPodcast("p1") {
		t.Fatal("toggle must be written to config.yaml immediately")
	}
	key(tea.KeyDown)
	key(tea.KeyEnter) // add Beta
	if len(a.cfg.Playlists[0].Podcasts) != 2 {
		t.Fatal("expected two podcasts")
	}
	key(tea.KeyTab) // remove Beta again
	onDisk, _ = config.Load()
	if onDisk.Playlists[0].HasPodcast("p2") || !onDisk.Playlists[0].HasPodcast("p1") {
		t.Fatalf("toggle off not persisted: %+v", onDisk.Playlists[0].Podcasts)
	}
}

// TestListShowsLastScheduledRun checks the "last run" column: blank for a
// playlist with no schedule, "not run yet" or "not installed" before the first
// scheduled run, a green tick with the age after a good run, and a red failure
// after a bad one. Manual syncs must not affect it.
func TestListShowsLastScheduledRun(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	t.Setenv("ABP_CONFIG", t.TempDir()+"/config.yaml")
	a := &app{
		cfg: &config.Config{Playlists: []config.Playlist{
			{ID: "good", Name: "Good", Limit: 5, Schedule: "0 * * * *"},
			{ID: "bad", Name: "Bad", Limit: 5, Schedule: "0 * * * *"},
			{ID: "fresh", Name: "Fresh", Limit: 5, Schedule: "0 * * * *"},
			{ID: "uninstalled", Name: "Uninstalled", Limit: 5, Schedule: "0 * * * *"},
			{ID: "manual", Name: "Manual", Limit: 5},
		}},
		state:    config.LoadState(),
		podcasts: map[string][]abp.LibraryItem{},
		// good, bad and fresh are in the crontab; "uninstalled" is not.
		managed: map[string]string{"good": "0 * * * *", "bad": "0 * * * *", "fresh": "0 * * * *"},
	}
	a.state.RecordScheduled("good", "updated · 5 items", nil)
	a.state.RecordScheduled("bad", "", errors.New("connection refused"))
	a.state.Update("manual", func(p *config.PlaylistState) { p.LastSync = time.Now(); p.Summary = "ok" })
	a.state.Update("fresh", func(p *config.PlaylistState) { p.LastSync = time.Now() }) // manual sync only

	r := newRoot(a, newList(a))
	r.Update(tea.WindowSizeMsg{Width: 130, Height: 24})
	rows := map[string]string{}
	for _, line := range strings.Split(r.View(), "\n") {
		for _, name := range []string{"Good", "Bad", "Fresh", "Uninstalled", "Manual"} {
			if strings.Contains(line, name+" ") && strings.Contains(line, "eps") {
				rows[name] = line
			}
		}
	}
	for name, want := range map[string]string{
		"Good":        "✓",
		"Bad":         "✗ failed",
		"Fresh":       "not run yet", // a manual sync is not a scheduled run
		"Uninstalled": "not installed",
		"Manual":      "—",
	} {
		if !strings.Contains(rows[name], want) {
			t.Errorf("%s row should contain %q:\n%s", name, want, rows[name])
		}
	}
	if !strings.Contains(r.View(), "LAST RUN") || !strings.Contains(r.View(), "LAST SYNC") {
		t.Error("expected both LAST SYNC and LAST RUN headings")
	}
}

// TestHeaderShowsVersionAndUpdate checks the header: the version is always
// there, an "update available" notice appears only when there is one, and no
// line is ever wider than the terminal, however narrow.
func TestHeaderShowsVersionAndUpdate(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	t.Setenv("ABP_CONFIG", t.TempDir()+"/config.yaml")
	a := &app{cfg: &config.Config{}, state: config.LoadState(), managed: map[string]string{},
		podcasts: map[string][]abp.LibraryItem{}, creds: config.Credentials{URL: "http://abs.local:13378", APIKey: "k"},
		build: version.Detect("v0.1.0")}
	r := newRoot(a, newList(a))
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 20})

	if out := r.View(); !strings.Contains(out, "v0.1.0") || strings.Contains(out, "available") {
		t.Fatalf("plain header wrong:\n%s", out)
	}
	r.Update(updateMsg{res: version.Result{Build: a.build, Status: version.UpdateAvailable, Latest: "v0.2.0"}})
	out := r.View()
	if !strings.Contains(out, "↑ v0.2.0 available") || !strings.Contains(out, "v0.1.0") {
		t.Fatalf("update notice missing:\n%s", out)
	}
	for _, w := range []int{80, 50, 30} {
		r.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		for _, line := range strings.Split(r.View(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d: line is %d columns wide: %q", w, got, line)
			}
		}
	}
}

// TestHeaderBeforeFirstConnection covers the very first launch, when no server
// URL has been saved yet: the header must still draw (it once panicked here).
func TestHeaderBeforeFirstConnection(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	a := &app{cfg: &config.Config{}, state: config.LoadState(), managed: map[string]string{},
		podcasts: map[string][]abp.LibraryItem{}, build: version.Detect("v0.1.0")}
	r := newRoot(a, newSetup(a, true))
	for _, w := range []int{120, 40, 10} {
		r.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		if r.View() == "" {
			t.Fatalf("empty view at width %d", w)
		}
	}
}
