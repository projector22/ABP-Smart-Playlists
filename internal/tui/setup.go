package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"abp/internal/abp"
	"abp/internal/config"
)

type setupResultMsg struct {
	creds    config.Credentials
	username string
	err      error
}

type setupScreen struct {
	a       *app
	first   bool
	url     textinput.Model
	key     textinput.Model
	focus   int
	testing bool
	err     string
}

// newSetup creates the server connection form. first is true on the very first
// run, when there is nothing to go back to and extra guidance is shown. An
// existing API key is never displayed; leaving the key box empty keeps it.
func newSetup(a *app, first bool) *setupScreen {
	u := textinput.New()
	u.Prompt = ""
	u.Placeholder = "http://192.168.1.10:13378"
	u.CharLimit = 200
	u.SetValue(a.creds.URL)
	u.Focus()

	k := textinput.New()
	k.Prompt = ""
	k.Placeholder = "paste your API key"
	k.CharLimit = 2000
	k.EchoMode = textinput.EchoPassword
	k.EchoCharacter = '•'
	if a.creds.APIKey != "" {
		k.Placeholder = "•••••• (leave empty to keep the current key)"
	}
	return &setupScreen{a: a, first: first, url: u, key: k}
}

// Title is the breadcrumb label: "Connect" on first run, otherwise "Server".
func (s *setupScreen) Title() string {
	if s.first {
		return "Connect"
	}
	return "Server"
}

// Init starts the text cursor blinking; there is no other start-up work.
func (s *setupScreen) Init() tea.Cmd { return textinput.Blink }

// Help returns the key hints for the footer (none while testing the connection).
func (s *setupScreen) Help() string {
	if s.testing {
		return ""
	}
	esc := "back"
	if s.first {
		esc = "quit"
	}
	return helpLine("tab", "next field", "enter", "connect", "esc", esc)
}

// setFocus moves focus to the URL box (0) or the API key box (1).
func (s *setupScreen) setFocus(f int) {
	s.focus = f
	s.url.Blur()
	s.key.Blur()
	if f == 0 {
		s.url.Focus()
	} else {
		s.key.Focus()
	}
}

// Update handles the connection test result (saving the credentials and moving
// on if it worked, showing the error if not) and the form's keys.
func (s *setupScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case setupResultMsg:
		s.testing = false
		clear := func() tea.Msg { return busyMsg{""} }
		if m.err != nil {
			s.err = m.err.Error()
			return s, clear
		}
		if os := config.SaveCredentials(m.creds); os != nil {
			s.err = "Could not save credentials: " + os.Error()
			return s, clear
		}
		s.a.connect(m.creds)
		ok := status("Connected to %s as %s", s.a.client.Base(), m.username)
		if s.first {
			return s, tea.Batch(clear, replace(newList(s.a)), ok)
		}
		return s, tea.Batch(clear, pop(), ok)

	case tea.KeyMsg:
		if s.testing {
			return s, nil
		}
		s.err = ""
		switch m.String() {
		case "esc":
			if s.first {
				return s, tea.Quit
			}
			return s, pop()
		case "tab", "down":
			s.setFocus(1 - s.focus)
			return s, nil
		case "shift+tab", "up":
			s.setFocus(1 - s.focus)
			return s, nil
		case "enter":
			if s.focus == 0 {
				s.setFocus(1)
				return s, nil
			}
			return s, s.submit()
		}
	}

	var cmd tea.Cmd
	if s.focus == 0 {
		s.url, cmd = s.url.Update(msg)
	} else {
		s.key, cmd = s.key.Update(msg)
	}
	return s, cmd
}

// submit validates the input and tests the connection in the background by
// fetching the current user. Nothing is saved unless the server accepts the key.
func (s *setupScreen) submit() tea.Cmd {
	url := abp.NormalizeURL(s.url.Value())
	key := strings.TrimSpace(s.key.Value())
	// An empty key box means "keep the key I already have" (it is never shown).
	if key == "" {
		key = s.a.creds.APIKey // keep existing
	}
	if url == "" {
		s.err = "Enter the Audiobookshelf URL."
		s.setFocus(0)
		return nil
	}
	if key == "" {
		s.err = "Enter an API key."
		s.setFocus(1)
		return nil
	}
	s.testing = true
	creds := config.Credentials{URL: url, APIKey: key}
	return busy("Connecting…", func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		me, err := abp.NewClient(url, key).Me(ctx)
		if err != nil {
			return setupResultMsg{err: err}
		}
		return setupResultMsg{creds: creds, username: me.Username}
	})
}

// View draws the URL and API key boxes, where the credentials are stored, and
// any error or progress message.
func (s *setupScreen) View(w, h int) string {
	label := func(l string, f int) string {
		if s.focus == f {
			return sSel.Render(l)
		}
		return sMuted.Render(l)
	}
	var b strings.Builder
	b.WriteString("\n")
	if s.first {
		b.WriteString(sTitle.Render("Welcome to abp") + "\n")
		b.WriteString(sText.Render("Smart playlists for your Audiobookshelf podcasts.") + "\n\n")
		b.WriteString(sMuted.Render("First, tell me where your server is and give me an API key.") + "\n")
		b.WriteString(sMuted.Render("(Audiobookshelf → Settings → API Keys, or your user's token.)") + "\n\n")
	}
	b.WriteString(label("Server URL", 0) + "\n" + s.url.View() + "\n\n")
	b.WriteString(label("API key", 1) + "\n" + s.key.View() + "\n\n")
	b.WriteString(sMuted.Render("Saved privately to "+config.CredentialsPath()+" (mode 0600),") + "\n")
	b.WriteString(sMuted.Render("not in ~/.config. ABP_URL / ABP_API_KEY env vars override it.") + "\n")
	if s.err != "" {
		b.WriteString("\n" + sErr.Render("✗ "+s.err) + "\n")
	}
	if s.testing {
		b.WriteString("\n" + sMuted.Render("Testing connection…") + "\n")
	}
	return b.String()
}
