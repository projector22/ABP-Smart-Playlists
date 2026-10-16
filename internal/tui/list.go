package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"abp/internal/config"
	"abp/internal/cron"
)

type listScreen struct {
	a       *app
	cur     int
	confirm *confirm
}

// newList creates the main screen listing all playlists.
func newList(a *app) *listScreen { return &listScreen{a: a} }

// Title is the breadcrumb label shown in the header for this screen.
func (s *listScreen) Title() string { return "Playlists" }

// Init does nothing; the list has no background work to start.
func (s *listScreen) Init() tea.Cmd { return nil }

// Help returns the key hints for the footer (hidden while a confirmation is
// open, which shows its own keys).
func (s *listScreen) Help() string {
	if s.confirm != nil {
		return ""
	}
	return helpLine("↑↓", "move", "enter", "open", "n", "new", "d", "delete", "s", "sync", "S", "sync all", "C", "crontab", "o", "server", "q", "quit")
}

// Update handles keys on the playlist list: navigation, opening, creating and
// deleting playlists, syncing one or all, reinstalling the crontab, opening the
// server settings and quitting.
func (s *listScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	if s.confirm != nil {
		cmd, done := s.confirm.handle(k)
		if done {
			s.confirm = nil
		}
		return s, cmd
	}
	pls := s.a.cfg.Playlists
	n := len(pls)
	s.cur = clamp(s.cur, 0, n-1)

	switch k.String() {
	case "up", "k":
		s.cur = clamp(s.cur-1, 0, n-1)
	case "down", "j":
		s.cur = clamp(s.cur+1, 0, n-1)
	case "enter", "right", "l":
		if n > 0 {
			return s, push(newDetail(s.a, pls[s.cur].ID))
		}
	case "n":
		return s, push(newForm(s.a, "", false))
	case "d":
		if n > 0 {
			id, name := pls[s.cur].ID, pls[s.cur].Name
			s.confirm = &confirm{
				prompt: fmt.Sprintf("Delete playlist %q?", name),
				opts: []confirmOpt{
					{"y", "delete + remove from Audiobookshelf", func() tea.Cmd { return s.a.deletePlaylist(id, true) }},
					{"k", "delete, keep the Audiobookshelf playlist", func() tea.Cmd { return s.a.deletePlaylist(id, false) }},
				},
			}
		}
	case "s":
		if n > 0 {
			return s, s.a.syncCmd(pls[s.cur].ID)
		}
	case "S":
		if n > 0 {
			return s, s.a.syncAllCmd()
		}
	case "C":
		if err := s.a.syncCron(); err != nil {
			return s, statusErr("crontab: %v", err)
		}
		return s, status("Crontab now matches your schedules")
	case "o":
		return s, push(newSetup(s.a, false))
	case "q":
		return s, tea.Quit
	}
	return s, nil
}

// scheduleBadge renders a playlist's schedule for the list: "manual" when it has
// none, a green dot when the schedule is installed in the crontab, and a hollow
// amber dot when it is configured but not (yet) installed.
func scheduleBadge(a *app, p config.Playlist) string {
	if p.Schedule == "" {
		return sMuted.Render("manual")
	}
	label := cron.Describe(p.Schedule)
	if a.managed[p.ID] == p.Schedule {
		return sOK.Render("● ") + sText.Render(label)
	}
	return sWarn.Render("○ ") + sText.Render(label)
}

// syncBadge renders the outcome of a playlist's last sync: failed, never run, or
// how long ago it succeeded.
func syncBadge(st config.PlaylistState) string {
	switch {
	case st.Error != "":
		return sErr.Render("✗ failed " + ago(st.LastSync))
	case st.LastSync.IsZero():
		return sMuted.Render("never synced")
	default:
		return sOK.Render("✓ ") + sMuted.Render(ago(st.LastSync))
	}
}

// View draws the playlist table (name, size, podcast count, schedule, last sync)
// with the selected row highlighted, the last result of the selected playlist,
// any crontab problem, and an open confirmation. With no playlists it shows a
// short welcome instead.
func (s *listScreen) View(w, h int) string {
	pls := s.a.cfg.Playlists
	if len(pls) == 0 {
		msg := sTitle.Render("No playlists yet") + "\n\n" +
			sText.Render("A smart playlist keeps itself filled with the oldest unfinished\n"+
				"episodes from the podcasts you pick, in publish order.") + "\n\n" +
			sMuted.Render("Press ") + sKey.Render("n") + sMuted.Render(" to create your first one.")
		return "\n" + sBox.Render(msg)
	}
	s.cur = clamp(s.cur, 0, len(pls)-1)

	const (
		wSize  = 9
		wPods  = 10
		wSched = 24
		wSync  = 18
	)
	wName := w - 2 - wSize - wPods - wSched - wSync
	if wName < 12 {
		wName = 12
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + sColHdr.Render(cell("PLAYLIST", wName)+cell("SIZE", wSize)+cell("PODCASTS", wPods)+cell("SCHEDULE", wSched)+"LAST SYNC") + "\n")

	avail := h - 4
	if s.confirm != nil {
		avail -= 5
	}
	start, end := window(s.cur, len(pls), avail)
	for i := start; i < end; i++ {
		p := pls[i]
		st := s.a.state.Get(p.ID)
		cursor, name := "  ", sText.Render(cell(p.Name, wName))
		if i == s.cur {
			cursor = sSel.Render("▸ ")
			name = sSel.Render(cell(p.Name, wName))
		}
		sched := scheduleBadge(s.a, p)
		sched += strings.Repeat(" ", max(0, wSched-visibleWidth(sched)))
		b.WriteString(cursor + name +
			sText.Render(cell(fmt.Sprintf("%d eps", p.Limit), wSize)) +
			sText.Render(cell(fmt.Sprintf("%d", len(p.Podcasts)), wPods)) +
			sched + syncBadge(st) + "\n")
	}
	if len(pls) > end-start {
		b.WriteString(sMuted.Render(fmt.Sprintf("\n  showing %d–%d of %d", start+1, end, len(pls))) + "\n")
	}
	if s.cur < len(pls) {
		sel := pls[s.cur]
		st := s.a.state.Get(sel.ID)
		if st.Error != "" {
			b.WriteString("\n" + sErr.Render("  "+trunc(st.Error, w-4)) + "\n")
		} else if st.Summary != "" {
			b.WriteString("\n" + sMuted.Render("  last result: "+st.Summary) + "\n")
		}
	}
	if s.a.cronErr != nil {
		b.WriteString("\n" + sWarn.Render("  crontab unavailable: "+trunc(s.a.cronErr.Error(), w-24)) + "\n")
	}
	if s.confirm != nil {
		b.WriteString("\n" + s.confirm.view())
	}
	return b.String()
}
