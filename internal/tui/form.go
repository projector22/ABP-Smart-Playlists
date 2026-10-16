package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"abp/internal/abp"
	"abp/internal/config"
	"abp/internal/cron"
)

type librariesLoadedMsg struct {
	libs []abp.Library
	err  error
}

const (
	fName = iota
	fDesc
	fLib
	fLimit
	fSched
	fCustom
)

type schedOpt struct {
	label, expr string
	custom      bool
}

type formScreen struct {
	a      *app
	editID string // empty when creating

	name, desc, limit, custom textinput.Model
	libs                      []abp.Library
	libIdx                    int
	opts                      []schedOpt
	schedIdx                  int
	focus                     int
	err                       string
	loading                   bool
}

// newForm builds the create (editID=="") or edit form.
func newForm(a *app, editID string, focusSchedule bool) *formScreen {
	mk := func(prompt, placeholder string, limit int) textinput.Model {
		t := textinput.New()
		t.Prompt = prompt
		t.Placeholder = placeholder
		t.CharLimit = limit
		return t
	}
	s := &formScreen{
		a:      a,
		editID: editID,
		name:   mk("", "e.g. History backlog", 60),
		desc:   mk("", "optional, shown in Audiobookshelf", 300),
		limit:  mk("", "25", 4),
		custom: mk("", "*/20 * * * *", 40),
		libs:   a.libs,
	}
	s.limit.SetValue(strconv.Itoa(config.DefaultLimit))

	s.opts = append(s.opts, schedOpt{label: "Off (manual sync only)"})
	for _, p := range cron.Presets {
		s.opts = append(s.opts, schedOpt{label: p.Label, expr: p.Expr})
	}
	s.opts = append(s.opts, schedOpt{label: "Custom cron expression…", custom: true})

	if pl := a.cfg.Find(editID); pl != nil {
		s.name.SetValue(pl.Name)
		s.desc.SetValue(pl.Description)
		s.limit.SetValue(strconv.Itoa(pl.Limit))
		s.schedIdx = 0
		if pl.Schedule != "" {
			s.schedIdx = len(s.opts) - 1
			s.custom.SetValue(pl.Schedule)
			for i, o := range s.opts {
				if o.expr == pl.Schedule {
					s.schedIdx = i
				}
			}
		}
	}
	if focusSchedule {
		s.focus = fSched
	}
	s.applyFocus()
	return s
}

// Title is the breadcrumb label, depending on whether a playlist is being
// created or edited.
func (s *formScreen) Title() string {
	if s.editID != "" {
		return "Edit"
	}
	return "New playlist"
}

// Init starts the cursor blinking and, when creating a playlist, loads the
// server's podcast libraries (unless already cached) so one can be chosen.
func (s *formScreen) Init() tea.Cmd {
	if s.editID != "" || len(s.libs) > 0 {
		return textinput.Blink
	}
	client := s.a.client
	s.loading = true
	return tea.Batch(textinput.Blink, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		libs, err := client.PodcastLibraries(ctx)
		return librariesLoadedMsg{libs, err}
	})
}

// Help returns the key hints for the footer.
func (s *formScreen) Help() string {
	return helpLine("tab", "next field", "←→", "change option", "enter", "next / save", "ctrl+s", "save", "esc", "cancel")
}

// fields returns the form fields currently visible, in tab order. The library
// choice only exists when creating (it cannot change afterwards), and the custom
// cron box only appears when "Custom cron expression" is selected.
func (s *formScreen) fields() []int {
	var f []int
	f = append(f, fName, fDesc)
	if s.editID == "" {
		f = append(f, fLib)
	}
	f = append(f, fLimit, fSched)
	if s.opts[s.schedIdx].custom {
		f = append(f, fCustom)
	}
	return f
}

// applyFocus gives keyboard focus to the text input of the focused field and
// removes it from the others. Fields that are selectors have no input to focus.
func (s *formScreen) applyFocus() {
	s.name.Blur()
	s.desc.Blur()
	s.limit.Blur()
	s.custom.Blur()
	switch s.focus {
	case fName:
		s.name.Focus()
	case fDesc:
		s.desc.Focus()
	case fLimit:
		s.limit.Focus()
	case fCustom:
		s.custom.Focus()
	}
}

// move shifts focus forward or backward through the visible fields, wrapping at
// either end.
func (s *formScreen) move(delta int) {
	f := s.fields()
	idx := 0
	for i, v := range f {
		if v == s.focus {
			idx = i
		}
	}
	idx = (idx + delta + len(f)) % len(f)
	s.focus = f[idx]
	s.applyFocus()
}

// Update handles messages for the form: the loaded libraries, navigation and
// save keys, left/right to cycle the selector fields, and otherwise forwards
// typing to the focused text input.
func (s *formScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case librariesLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.err = "Could not load libraries: " + m.err.Error()
			return s, nil
		}
		s.libs = m.libs
		s.a.libs = m.libs
		if len(m.libs) == 0 {
			s.err = "No podcast libraries found on this server."
		}
		return s, nil

	case tea.KeyMsg:
		s.err = ""
		switch m.String() {
		case "esc":
			return s, pop()
		case "ctrl+s":
			return s.save()
		case "tab", "down":
			s.move(1)
			return s, nil
		case "shift+tab", "up":
			s.move(-1)
			return s, nil
		case "enter":
			f := s.fields()
			if s.focus == f[len(f)-1] {
				return s.save()
			}
			s.move(1)
			return s, nil
		case "left", "right":
			d := 1
			if m.String() == "left" {
				d = -1
			}
			switch s.focus {
			case fLib:
				if n := len(s.libs); n > 0 {
					s.libIdx = (s.libIdx + d + n) % n
				}
				return s, nil
			case fSched:
				n := len(s.opts)
				s.schedIdx = (s.schedIdx + d + n) % n
				return s, nil
			}
		}
	}

	var cmd tea.Cmd
	switch s.focus {
	case fName:
		s.name, cmd = s.name.Update(msg)
	case fDesc:
		s.desc, cmd = s.desc.Update(msg)
	case fLimit:
		s.limit, cmd = s.limit.Update(msg)
	case fCustom:
		s.custom, cmd = s.custom.Update(msg)
	}
	return s, cmd
}

// save validates the form and writes it. Validation failures stay on the form
// with the offending field focused. When editing, the playlist is updated in
// place; when creating, a new playlist is appended, the screen is replaced by
// the playlist's detail view, and the podcast picker opens so members can be
// chosen straight away. Any schedule change is applied to the crontab.
func (s *formScreen) save() (screen, tea.Cmd) {
	name := strings.TrimSpace(s.name.Value())
	if name == "" {
		s.err, s.focus = "Give the playlist a name.", fName
		s.applyFocus()
		return s, nil
	}
	limit, err := strconv.Atoi(strings.TrimSpace(s.limit.Value()))
	if err != nil || limit < 1 || limit > 1000 {
		s.err, s.focus = "Size must be a number between 1 and 1000.", fLimit
		s.applyFocus()
		return s, nil
	}
	desc := strings.TrimSpace(s.desc.Value())
	// The schedule is either empty (manual), a preset's expression, or a custom
	// expression that must pass cron validation before we accept it.
	opt := s.opts[s.schedIdx]
	sched := opt.expr
	if opt.custom {
		sched = strings.Join(strings.Fields(s.custom.Value()), " ")
		if err := cron.Validate(sched); err != nil {
			s.err, s.focus = "Schedule: "+err.Error(), fCustom
			s.applyFocus()
			return s, nil
		}
	}

	if s.editID != "" {
		pl := s.a.cfg.Find(s.editID)
		if pl == nil {
			return s, pop()
		}
		pl.Name, pl.Description, pl.Limit, pl.Schedule = name, desc, limit, sched
		if err := saveCfg(s.a); err != nil {
			s.err = "Could not save: " + err.Error()
			return s, nil
		}
		if err := s.a.syncCron(); err != nil {
			return s, tea.Batch(pop(), statusErr("Saved, but crontab: %v", err))
		}
		return s, tea.Batch(pop(), status("Saved %q · press s to sync", name))
	}

	if len(s.libs) == 0 {
		s.err = "No podcast library available yet."
		return s, nil
	}
	lib := s.libs[s.libIdx]
	id := s.a.cfg.UniqueID(name)
	s.a.cfg.Playlists = append(s.a.cfg.Playlists, config.Playlist{
		ID: id, Name: name, Description: desc, Library: lib.ID, LibraryName: lib.Name,
		Limit: limit, Schedule: sched, Podcasts: []config.Podcast{},
	})
	if err := saveCfg(s.a); err != nil {
		s.a.cfg.Remove(id)
		s.err = "Could not save: " + err.Error()
		return s, nil
	}
	cmds := []tea.Cmd{replace(newDetail(s.a, id)), push(newPicker(s.a, id))}
	if err := s.a.syncCron(); err != nil {
		cmds = append(cmds, statusErr("Created, but crontab: %v", err))
	}
	return s, tea.Sequence(cmds...)
}

// View draws the labelled form fields with hints, plus any loading or error
// message.
func (s *formScreen) View(w, h int) string {
	var b strings.Builder
	b.WriteString("\n")

	row := func(f int, label, body, hint string) {
		l := sMuted.Render(cell(label, 12))
		marker := "  "
		if s.focus == f {
			l = sSel.Render(cell(label, 12))
			marker = sSel.Render("▸ ")
		}
		b.WriteString(marker + l + body + "\n")
		if hint != "" {
			b.WriteString("  " + cell("", 12) + sMuted.Render(hint) + "\n")
		}
		b.WriteString("\n")
	}
	cycle := func(f int, text string) string {
		if s.focus == f {
			return sKey.Render("◀ ") + sText.Render(text) + sKey.Render(" ▶")
		}
		return sText.Render(text)
	}

	row(fName, "Name", s.name.View(), "")
	row(fDesc, "Description", s.desc.View(), "")

	if s.editID == "" {
		lib := "loading…"
		if len(s.libs) > 0 {
			lib = s.libs[s.libIdx].Name
		}
		row(fLib, "Library", cycle(fLib, lib), "Podcasts must come from this library (can't be changed later).")
	} else if pl := s.a.cfg.Find(s.editID); pl != nil {
		lib := pl.LibraryName
		if lib == "" {
			lib = pl.Library
		}
		b.WriteString("  " + sMuted.Render(cell("Library", 12)) + sMuted.Render(lib) + "\n\n")
	}

	row(fLimit, "Size", s.limit.View(), "Maximum number of unfinished episodes kept in the playlist.")

	hint := ""
	opt := s.opts[s.schedIdx]
	if opt.expr != "" {
		hint = "Runs `abp sync` via your crontab: " + opt.expr
	}
	row(fSched, "Schedule", cycle(fSched, opt.label), hint)
	if opt.custom {
		row(fCustom, "Cron", s.custom.View(), "minute hour day-of-month month day-of-week")
	}

	switch {
	case s.loading:
		b.WriteString(sMuted.Render("Loading libraries…"))
	case s.err != "":
		b.WriteString(sErr.Render("✗ " + s.err))
	}
	return b.String()
}
