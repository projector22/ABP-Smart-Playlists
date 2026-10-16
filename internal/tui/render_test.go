package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"abp/internal/abp"
	"abp/internal/config"
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
