package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"abp/internal/engine"
)

type previewMsg struct {
	id  string
	q   *engine.Queue
	err error
}

type previewScreen struct {
	a    *app
	id   string
	q    *engine.Queue
	err  error
	cur  int
	done bool
}

// newPreview creates a screen showing what a playlist would contain right now.
func newPreview(a *app, id string) *previewScreen { return &previewScreen{a: a, id: id} }

// Title is the breadcrumb label shown in the header for this screen.
func (s *previewScreen) Title() string { return "Preview" }

// Init builds the queue in the background. Nothing is written to the server.
func (s *previewScreen) Init() tea.Cmd {
	p := s.a.cfg.Find(s.id)
	if p == nil {
		return nil
	}
	pl, client, id := *p, s.a.client, s.id
	return busy("Building queue…", func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		q, err := engine.BuildQueue(ctx, client, pl)
		return previewMsg{id, q, err}
	})
}

// Help returns the key hints for the footer.
func (s *previewScreen) Help() string {
	return helpLine("↑↓", "scroll", "s", "sync now", "esc", "back")
}

// Update handles the finished queue build, scrolling, going back and syncing
// straight from the preview.
func (s *previewScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case previewMsg:
		if m.id != s.id {
			return s, nil
		}
		s.q, s.err, s.done = m.q, m.err, true
		clear := func() tea.Msg { return busyMsg{""} }
		if m.err != nil {
			return s, tea.Batch(clear, statusErr("Preview failed: %v", m.err))
		}
		return s, clear
	case tea.KeyMsg:
		n := 0
		if s.q != nil {
			n = len(s.q.Items)
		}
		switch m.String() {
		case "esc", "q", "left", "h":
			return s, pop()
		case "up", "k":
			s.cur = clamp(s.cur-1, 0, n-1)
		case "down", "j":
			s.cur = clamp(s.cur+1, 0, n-1)
		case "pgup":
			s.cur = clamp(s.cur-10, 0, n-1)
		case "pgdown":
			s.cur = clamp(s.cur+10, 0, n-1)
		case "s":
			return s, s.a.syncCmd(s.id)
		}
	}
	return s, nil
}

// View draws a header with how many of the unfinished episodes fit within the
// limit, any warnings, and a scrollable table of the episodes in playlist order.
func (s *previewScreen) View(w, h int) string {
	var b strings.Builder
	b.WriteString("\n")
	switch {
	case !s.done:
		return b.String() + sMuted.Render("Working out what would be in the playlist…")
	case s.err != nil:
		return b.String() + sErr.Render(s.err.Error())
	case len(s.q.Items) == 0:
		return b.String() + sMuted.Render("Nothing to queue: every episode from these podcasts is finished (or no podcasts added yet).")
	}

	pl := s.a.cfg.Find(s.id)
	limit := 0
	if pl != nil {
		limit = pl.Limit
	}
	b.WriteString(sText.Render(fmt.Sprintf("%d of %d unfinished episodes (limit %d), oldest first",
		len(s.q.Items), s.q.Unfinished, limit)) + "\n")
	for _, wmsg := range s.q.Warnings {
		b.WriteString(sWarn.Render("! "+trunc(wmsg, w-2)) + "\n")
	}
	b.WriteString("\n")

	const (
		wNum  = 4
		wDate = 12
		wDur  = 8
	)
	wPod := (w - wNum - wDate - wDur) / 3
	wTitle := w - wNum - wDate - wDur - wPod
	b.WriteString(sColHdr.Render(cell("#", wNum)+cell("PUBLISHED", wDate)+cell("PODCAST", wPod)+cell("EPISODE", wTitle)+"LENGTH") + "\n")

	start, end := window(s.cur, len(s.q.Items), h-7)
	for i := start; i < end; i++ {
		it := s.q.Items[i]
		line := cell(fmt.Sprint(i+1), wNum) + cell(it.Published.Format("2006-01-02"), wDate) +
			cell(it.Podcast, wPod) + cell(it.Title, wTitle) + fmtDuration(it.Seconds)
		if i == s.cur {
			b.WriteString(sSel.Render(line) + "\n")
		} else {
			b.WriteString(sText.Render(line) + "\n")
		}
	}
	return b.String()
}
