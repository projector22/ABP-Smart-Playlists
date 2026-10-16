package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DataDir is where private data lives (never under ~/.config).
// Override with ABP_DATA_DIR.
func DataDir() string {
	if d := os.Getenv("ABP_DATA_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "abp")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "abp")
}

// CredentialsPath is the location of the private credentials file (server URL
// and API key), inside the data directory.
func CredentialsPath() string { return filepath.Join(DataDir(), "credentials.json") }

// StatePath is the location of the bookkeeping file (server-side playlist ids
// and last-sync results), inside the data directory.
func StatePath() string { return filepath.Join(DataDir(), "state.json") }

// LogPath is the file that cron output is appended to, inside the data
// directory.
func LogPath() string { return filepath.Join(DataDir(), "abp.log") }

type Credentials struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

// Valid reports whether both a server URL and an API key are present.
func (c Credentials) Valid() bool { return c.URL != "" && c.APIKey != "" }

// LoadCredentials reads the private credentials file. ABP_URL / ABP_API_KEY
// environment variables take precedence (handy for secret managers).
func LoadCredentials() (Credentials, error) {
	var c Credentials
	b, err := os.ReadFile(CredentialsPath())
	if err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if v := os.Getenv("ABP_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("ABP_API_KEY"); v != "" {
		c.APIKey = v
	}
	c.URL = strings.TrimSpace(c.URL)
	c.APIKey = strings.TrimSpace(c.APIKey)
	return c, nil
}

// SaveCredentials stores the credentials in the private data directory, readable
// only by the current user (file mode 0600, directory mode 0700).
func SaveCredentials(c Credentials) error {
	return writePrivate(CredentialsPath(), c)
}

// writePrivate marshals v as indented JSON and writes it to path atomically with
// owner-only permissions. The directory is created (and tightened to 0700) if
// needed; the temp-file-then-rename dance prevents a partially written file.
func writePrivate(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- state -----------------------------------------------------------------

type PlaylistState struct {
	ABSID    string    `json:"abs_id,omitempty"`
	LastSync time.Time `json:"last_sync,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type State struct {
	mu        sync.Mutex
	Playlists map[string]*PlaylistState `json:"playlists"`
}

// LoadState reads the bookkeeping file. A missing or unreadable file simply
// yields an empty State: the data is only a cache of things abp can rediscover
// from the server.
func LoadState() *State {
	s := &State{Playlists: map[string]*PlaylistState{}}
	if b, err := os.ReadFile(StatePath()); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Playlists == nil {
		s.Playlists = map[string]*PlaylistState{}
	}
	return s
}

// Get returns a copy of the stored state for one playlist (zero value if none).
// A copy is returned so callers can read it without holding the lock.
func (s *State) Get(id string) PlaylistState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.Playlists[id]; p != nil {
		return *p
	}
	return PlaylistState{}
}

// Update mutates the state for one playlist and persists it.
func (s *State) Update(id string, fn func(*PlaylistState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Playlists[id]
	if p == nil {
		p = &PlaylistState{}
		s.Playlists[id] = p
	}
	fn(p)
	_ = writePrivate(StatePath(), s)
}

// Forget drops everything remembered about a playlist and persists the change.
// It is used when a playlist is deleted.
func (s *State) Forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Playlists, id)
	_ = writePrivate(StatePath(), s)
}
