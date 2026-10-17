package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/projector22/abp/internal/cron"
)

type detailScreen struct {
	a       *app
	id      string
	cur     int
	confirm *confirm
}

// newDetail creates the screen showing one playlist, looked up by id each time
// it is drawn so edits made elsewhere are always reflected.
func newDetail(a *app, id string) *detailScreen { return &detailScreen{a: a, id: id} }

// Init does nothing; the detail screen has no background work to start.
func (s *detailScreen) Init() tea.Cmd { return nil }

// Title is the breadcrumb label: the playlist's name.
func (s *detailScreen) Title() string {
	if p := s.a.cfg.Find(s.id); p != nil {
		return p.Name
	}
	return "Playlist"
}

// Help returns the key hints for the footer (hidden while a confirmation is
// open, which shows its own keys).
func (s *detailScreen) Help() string {
	if s.confirm != nil {
		return ""
	}
	return helpLine("a", "podcasts", "x", "remove", "e", "edit", "c", "schedule", "p", "preview", "s", "sync", "D", "delete", "esc", "back")
}

// Update handles keys on the playlist screen: moving through the podcast list,
// opening the podcast picker, edit/schedule forms and the preview, syncing, and
// the confirmed destructive actions (remove a podcast, delete the playlist).
func (s *detailScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	pl := s.a.cfg.Find(s.id)
	if pl == nil {
		return s, pop()
	}
	if s.confirm != nil {
		cmd, done := s.confirm.handle(k)
		if done {
			s.confirm = nil
		}
		return s, cmd
	}
	n := len(pl.Podcasts)
	s.cur = clamp(s.cur, 0, n-1)

	switch k.String() {
	case "esc", "left", "h", "q":
		return s, pop()
	case "up", "k":
		s.cur = clamp(s.cur-1, 0, n-1)
	case "down", "j":
		s.cur = clamp(s.cur+1, 0, n-1)
	case "a":
		return s, push(newPicker(s.a, s.id))
	case "x", "delete", "backspace":
		if n > 0 {
			podID, title := pl.Podcasts[s.cur].ID, pl.Podcasts[s.cur].Title
			s.confirm = &confirm{
				prompt: fmt.Sprintf("Remove %q from this playlist?", title),
				opts: []confirmOpt{{"y", "remove", func() tea.Cmd {
					p := s.a.cfg.Find(s.id)
					if p == nil {
						return nil
					}
					p.RemovePodcast(podID)
					if err := saveCfg(s.a); err != nil {
						return statusErr("Could not save: %v", err)
					}
					return status("Removed %q · press s to sync", title)
				}}, cancelOpt()},
			}
		}
	case "e":
		return s, push(newForm(s.a, s.id, false))
	case "c":
		return s, push(newForm(s.a, s.id, true))
	case "p":
		return s, push(newPreview(s.a, s.id))
	case "s":
		return s, s.a.syncCmd(s.id)
	case "D":
		name := pl.Name
		id := s.id
		s.confirm = &confirm{
			prompt: fmt.Sprintf("Delete playlist %q?", name),
			opts: []confirmOpt{
				{"y", "delete + remove from Audiobookshelf", func() tea.Cmd {
					return tea.Batch(s.a.deletePlaylist(id, true), pop())
				}},
				{"k", "delete, keep the Audiobookshelf playlist", func() tea.Cmd {
					return tea.Batch(s.a.deletePlaylist(id, false), pop())
				}},
			},
		}
	}
	return s, nil
}

// View draws the playlist's details (description, library, size, schedule and
// whether it is installed in the crontab, last sync) followed by a scrollable
// list of its podcasts.
func (s *detailScreen) View(w, h int) string {
	pl := s.a.cfg.Find(s.id)
	if pl == nil {
		return sMuted.Render("Playlist not found.")
	}
	st := s.a.state.Get(pl.ID)

	label := func(l string) string { return sMuted.Render(cell(l, 11)) }

	lib := pl.LibraryName
	if lib == "" {
		lib = pl.Library
	}
	sched := sMuted.Render("manual only — press c to add a schedule")
	if pl.Schedule != "" {
		state := sWarn.Render("○ not in crontab (press C on the list to install)")
		if s.a.managed[pl.ID] == pl.Schedule {
			state = sOK.Render("● installed")
		}
		sched = sText.Render(cron.Describe(pl.Schedule)) + sMuted.Render("  ("+pl.Schedule+")  ") + state
	}
	last := sMuted.Render("never synced")
	switch {
	case st.Error != "":
		last = sErr.Render("✗ " + trunc(st.Error, w-14))
	case !st.LastSync.IsZero():
		last = sOK.Render("✓ ") + sText.Render(ago(st.LastSync)) + sMuted.Render(" — "+st.Summary)
	}

	var b strings.Builder
	b.WriteString("\n" + sTitle.Render(pl.Name) + "\n\n")
	if pl.Description != "" {
		b.WriteString(label("About") + sText.Render(trunc(pl.Description, w-12)) + "\n")
	}
	b.WriteString(label("Library") + sText.Render(lib) + "\n")
	b.WriteString(label("Size") + sText.Render(fmt.Sprintf("up to %d unfinished episodes, oldest first", pl.Limit)) + "\n")
	b.WriteString(label("Schedule") + sched + "\n")
	b.WriteString(label("Last sync") + last + "\n\n")

	b.WriteString(sColHdr.Render(fmt.Sprintf("PODCASTS (%d)", len(pl.Podcasts))) + "\n")
	if len(pl.Podcasts) == 0 {
		b.WriteString(sMuted.Render("  none yet — press ") + sKey.Render("a") + sMuted.Render(" to add some") + "\n")
	}
	s.cur = clamp(s.cur, 0, len(pl.Podcasts)-1)
	avail := h - 11
	if pl.Description != "" {
		avail--
	}
	if s.confirm != nil {
		avail -= 5
	}
	start, end := window(s.cur, len(pl.Podcasts), avail)
	for i := start; i < end; i++ {
		t := trunc(pl.Podcasts[i].Title, w-4)
		if i == s.cur {
			b.WriteString(sSel.Render("▸ "+t) + "\n")
		} else {
			b.WriteString("  " + sText.Render(t) + "\n")
		}
	}
	if len(pl.Podcasts) > end-start {
		b.WriteString(sMuted.Render(fmt.Sprintf("  … %d–%d of %d", start+1, end, len(pl.Podcasts))) + "\n")
	}
	if s.confirm != nil {
		b.WriteString("\n" + s.confirm.view())
	}
	return b.String()
}
