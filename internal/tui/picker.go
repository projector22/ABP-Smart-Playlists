package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"abp/internal/abp"
	"abp/internal/config"
)

type podcastsLoadedMsg struct {
	lib   string
	items []abp.LibraryItem
	err   error
}

// pickerScreen lists every podcast in the playlist's library. Toggling a row
// adds/removes it from the playlist and writes config.yaml straight away, so
// there is nothing to confirm and you stay on this screen until you press esc.
type pickerScreen struct {
	a      *app
	id     string
	libID  string
	loaded bool
	err    error
	items  []abp.LibraryItem
	view   []int // indexes into items that match the filter
	cur    int
	in     textinput.Model
	last   string
}

// newPicker creates the podcast picker for the playlist with the given id. The
// filter box is focused immediately so typing narrows the list.
func newPicker(a *app, id string) *pickerScreen {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "type to filter"
	ti.CharLimit = 80
	ti.Focus()
	s := &pickerScreen{a: a, id: id, in: ti}
	if pl := a.cfg.Find(id); pl != nil {
		s.libID = pl.Library
	}
	return s
}

// Title is the breadcrumb label shown in the header for this screen.
func (s *pickerScreen) Title() string { return "Podcasts" }

// Init shows the library's podcasts from the cache when available, otherwise
// fetches them from the server in the background.
func (s *pickerScreen) Init() tea.Cmd {
	if items, ok := s.a.podcasts[s.libID]; ok {
		s.items, s.loaded = items, true
		s.refilter()
		return textinput.Blink
	}
	client, lib := s.a.client, s.libID
	return tea.Batch(textinput.Blink, busy("Loading podcasts…", func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		items, err := client.LibraryPodcasts(ctx, lib)
		return podcastsLoadedMsg{lib, items, err}
	}))
}

// Help returns the key hints for the footer.
func (s *pickerScreen) Help() string {
	return helpLine("↑↓", "move", "tab/enter", "toggle", "esc", "done")
}

// refilter rebuilds the visible list from the current filter text, matching
// case-insensitively against title and author, and keeps the cursor in range.
func (s *pickerScreen) refilter() {
	q := strings.ToLower(strings.TrimSpace(s.in.Value()))
	s.view = s.view[:0]
	for i, it := range s.items {
		if q == "" || strings.Contains(strings.ToLower(it.Title+" "+it.Author), q) {
			s.view = append(s.view, i)
		}
	}
	s.cur = clamp(s.cur, 0, len(s.view)-1)
}

// Update handles the picker: the loaded podcast list, navigation keys, toggling
// the highlighted podcast, and otherwise typing into the filter box.
func (s *pickerScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case podcastsLoadedMsg:
		if m.lib != s.libID {
			return s, nil
		}
		clear := func() tea.Msg { return busyMsg{""} }
		if m.err != nil {
			s.err = m.err
			return s, tea.Batch(clear, statusErr("Could not load podcasts: %v", m.err))
		}
		s.a.podcasts[m.lib] = m.items
		s.items, s.loaded = m.items, true
		s.refilter()
		return s, clear

	case tea.KeyMsg:
		n := len(s.view)
		switch m.String() {
		case "esc":
			return s, pop()
		case "up":
			s.cur = clamp(s.cur-1, 0, n-1)
			return s, nil
		case "down":
			s.cur = clamp(s.cur+1, 0, n-1)
			return s, nil
		case "pgup":
			s.cur = clamp(s.cur-10, 0, n-1)
			return s, nil
		case "pgdown":
			s.cur = clamp(s.cur+10, 0, n-1)
			return s, nil
		case "tab", "enter":
			return s, s.toggle()
		}
	}

	var cmd tea.Cmd
	s.in, cmd = s.in.Update(msg)
	if s.in.Value() != s.last {
		s.last = s.in.Value()
		s.cur = 0
		s.refilter()
	}
	return s, cmd
}

// toggle flips the highlighted podcast in/out of the playlist and saves.
func (s *pickerScreen) toggle() tea.Cmd {
	pl := s.a.cfg.Find(s.id)
	if pl == nil {
		return pop()
	}
	if len(s.view) == 0 {
		return nil
	}
	it := s.items[s.view[s.cur]]
	// Keep a copy of the member list so a failed save can be rolled back and the
	// screen never shows a state that isn't on disk.
	was := append([]config.Podcast(nil), pl.Podcasts...)

	var msg string
	if pl.HasPodcast(it.ID) {
		pl.RemovePodcast(it.ID)
		msg = fmt.Sprintf("Removed %q", it.Title)
	} else {
		pl.AddPodcasts(config.Podcast{ID: it.ID, Title: it.Title})
		msg = fmt.Sprintf("Added %q", it.Title)
	}
	if err := saveCfg(s.a); err != nil {
		pl.Podcasts = was // keep screen and disk consistent
		return statusErr("Could not save: %v", err)
	}
	return status("%s · press s on the playlist to sync", msg)
}

// View draws the filter box and the scrollable podcast list with a checkbox for
// each (ticked and green when it is in the playlist), plus a summary line.
func (s *pickerScreen) View(w, h int) string {
	var b strings.Builder
	b.WriteString("\n" + s.in.View() + "\n\n")
	switch {
	case s.err != nil:
		return b.String() + sErr.Render("Could not load podcasts: "+s.err.Error())
	case !s.loaded:
		return b.String() + sMuted.Render("Loading…")
	case len(s.view) == 0:
		return b.String() + sMuted.Render("No podcasts match.")
	}

	pl := s.a.cfg.Find(s.id)
	start, end := window(s.cur, len(s.view), h-7)
	for i := start; i < end; i++ {
		it := s.items[s.view[i]]
		in := pl != nil && pl.HasPodcast(it.ID)

		box := "[ ]"
		style := sText
		if in {
			box, style = "[✓]", sOK
		}
		text := box + " " + trunc(it.Title, max(10, w-6-min(len(it.Author), 30)-2))
		author := ""
		if it.Author != "" {
			author = sMuted.Render("  " + trunc(it.Author, 30))
		}
		if i == s.cur {
			b.WriteString(sSel.Render("▸ "+text) + author + "\n")
		} else {
			b.WriteString("  " + style.Render(text) + author + "\n")
		}
	}
	count := 0
	if pl != nil {
		count = len(pl.Podcasts)
	}
	b.WriteString("\n" + sMuted.Render(fmt.Sprintf("%d of %d shown · %s in this playlist", len(s.view), len(s.items), pluralize(count, "podcast", "podcasts"))))
	return b.String()
}
