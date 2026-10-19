// Package config holds everything abp persists locally:
//
//   - config.yaml       (human readable: playlists + their podcasts)  -> ~/.config/abp
//   - credentials.json  (server URL + API key, mode 0600)             -> ~/.local/share/abp
//   - state.json        (bookkeeping: ABS playlist ids, last sync)    -> ~/.local/share/abp
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultLimit = 25

type Podcast struct {
	ID    string `yaml:"id"` // Audiobookshelf library item id
	Title string `yaml:"title"`
}

type Playlist struct {
	ID          string    `yaml:"id"` // stable slug, used by `abp sync <id>` and cron
	Name        string    `yaml:"name"`
	Description string    `yaml:"description,omitempty"`
	Library     string    `yaml:"library"` // Audiobookshelf library id
	LibraryName string    `yaml:"library_name,omitempty"`
	Limit       int       `yaml:"limit"`              // max episodes in the playlist
	Schedule    string    `yaml:"schedule,omitempty"` // cron expression; empty = manual only
	Podcasts    []Podcast `yaml:"podcasts"`
}

type Config struct {
	Playlists []Playlist `yaml:"playlists"`
}

// Path returns the location of config.yaml (override with ABP_CONFIG).
func Path() string {
	if p := os.Getenv("ABP_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "abp", "config.yaml")
}

// Load reads config.yaml. A missing file is not an error: it yields an empty
// Config, which is the first-run state. Defaults are filled in for hand-edited
// files: a missing limit becomes DefaultLimit and a missing id is derived from
// the playlist name.
func Load() (*Config, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(), err)
	}
	for i := range c.Playlists {
		p := &c.Playlists[i]
		if p.Limit <= 0 {
			p.Limit = DefaultLimit
		}
		if p.ID == "" {
			p.ID = Slug(p.Name)
		}
	}
	return &c, nil
}

// Save writes the config as human-readable YAML with an explanatory header. The
// write is atomic (temp file, then rename) so a crash or a full disk can never
// leave a half-written config.yaml behind.
func Save(c *Config) error {
	var sb strings.Builder
	sb.WriteString("# abp playlists. Safe to edit by hand while abp is not running.\n")
	sb.WriteString("# schedule is a standard 5-field cron expression (empty = manual only).\n\n")
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	// Close flushes the encoder; if it fails the YAML may be incomplete, so never
	// write it out.
	if err := enc.Close(); err != nil {
		return err
	}

	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Find returns a pointer to the playlist with the given id, or nil if there is
// none. The pointer aims into the Playlists slice, so it is only valid until the
// slice is next appended to or reordered; look it up again rather than keeping
// it.
func (c *Config) Find(id string) *Playlist {
	for i := range c.Playlists {
		if c.Playlists[i].ID == id {
			return &c.Playlists[i]
		}
	}
	return nil
}

// Remove deletes the playlist with the given id. It is a no-op if the id is
// unknown. The filtering reuses the slice's backing array to avoid allocating.
func (c *Config) Remove(id string) {
	out := c.Playlists[:0]
	for _, p := range c.Playlists {
		if p.ID != id {
			out = append(out, p)
		}
	}
	c.Playlists = out
}

// UniqueID makes a slug for name that doesn't clash with an existing playlist.
func (c *Config) UniqueID(name string) string {
	base := Slug(name)
	id := base
	for n := 2; c.Find(id) != nil; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a display name into a lowercase, dash-separated id that is safe to
// use on a command line and in a crontab entry, e.g. "History Backlog!" becomes
// "history-backlog". A name with no usable characters becomes "playlist".
func Slug(s string) string {
	s = strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "playlist"
	}
	return s
}

// HasPodcast reports whether the podcast with the given library item id is a
// member of the playlist.
func (p *Playlist) HasPodcast(id string) bool {
	for _, x := range p.Podcasts {
		if x.ID == id {
			return true
		}
	}
	return false
}

// AddPodcasts adds podcasts to the playlist, ignoring any that are already
// members, and keeps the member list sorted by title (case-insensitively) so the
// YAML stays tidy and diffs stay small.
func (p *Playlist) AddPodcasts(add ...Podcast) {
	for _, a := range add {
		if !p.HasPodcast(a.ID) {
			p.Podcasts = append(p.Podcasts, a)
		}
	}
	sort.SliceStable(p.Podcasts, func(i, j int) bool {
		return strings.ToLower(p.Podcasts[i].Title) < strings.ToLower(p.Podcasts[j].Title)
	})
}

// RemovePodcast removes the podcast with the given library item id from the
// playlist. It is a no-op if the podcast is not a member.
func (p *Playlist) RemovePodcast(id string) {
	out := p.Podcasts[:0]
	for _, x := range p.Podcasts {
		if x.ID != id {
			out = append(out, x)
		}
	}
	p.Podcasts = out
}
