// Package abp is a minimal Audiobookshelf API client.
package abp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

type Client struct {
	base string
	key  string
	hc   *http.Client
}

// NormalizeURL trims whitespace/trailing slashes and defaults to http://.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

// NewClient returns a Client for the Audiobookshelf server at rawURL. Every
// request is authenticated with key, sent as a Bearer token. The URL is tidied
// with NormalizeURL, so "abs.local:13378/" and "http://abs.local:13378" are
// equivalent.
func NewClient(rawURL, key string) *Client {
	return &Client{
		base: NormalizeURL(rawURL),
		key:  strings.TrimSpace(key),
		hc:   &http.Client{Timeout: 60 * time.Second},
	}
}

// Base returns the normalised server URL this client talks to. It is used by the
// UI to show which server abp is connected to.
func (c *Client) Base() string { return c.base }

// do performs a single JSON request against the server.
//
// If in is non-nil it is encoded as the request body. If out is non-nil the
// response body is decoded into it (otherwise the body is discarded). Error
// handling is centralised here: a 404 is wrapped around ErrNotFound so callers
// can test for it with errors.Is, a 401 produces a hint about the API key, and
// any other non-2xx status becomes an error carrying a snippet of the response
// body to make server-side problems diagnosable.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // read-only body: a close error is not actionable

	// Map HTTP failures to useful errors before touching the body.
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("%s %s: %w", method, path, ErrNotFound)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("unauthorized (401): check the API key")
	case resp.StatusCode >= 300:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---- user / progress -------------------------------------------------------

type Progress struct {
	LibraryItemID string `json:"libraryItemId"`
	EpisodeID     string `json:"episodeId"`
	IsFinished    bool   `json:"isFinished"`
}

type Me struct {
	Username      string     `json:"username"`
	MediaProgress []Progress `json:"mediaProgress"`
}

// Me returns the authenticated user. Its MediaProgress list records which
// episodes the user has finished, which is how abp decides what to leave out of
// a playlist.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var m Me
	if err := c.do(ctx, "GET", "/api/me", nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---- libraries -------------------------------------------------------------

type Library struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
}

// PodcastLibraries returns only libraries that hold podcasts.
func (c *Client) PodcastLibraries(ctx context.Context) ([]Library, error) {
	var r struct {
		Libraries []Library `json:"libraries"`
	}
	if err := c.do(ctx, "GET", "/api/libraries", nil, &r); err != nil {
		return nil, err
	}
	var out []Library
	for _, l := range r.Libraries {
		if l.MediaType == "podcast" {
			out = append(out, l)
		}
	}
	return out, nil
}

type LibraryItem struct {
	ID     string `json:"id"`
	Title  string
	Author string
	Media  struct {
		Metadata struct {
			Title  string `json:"title"`
			Author string `json:"author"`
		} `json:"metadata"`
	} `json:"media"`
}

// LibraryPodcasts lists every podcast in a library (one entry per podcast, not
// per episode). It requests the minified form with no paging limit, sorted by
// title, then copies the title and author out of the nested metadata onto the
// flat Title and Author fields for convenience.
func (c *Client) LibraryPodcasts(ctx context.Context, libID string) ([]LibraryItem, error) {
	var r struct {
		Results []LibraryItem `json:"results"`
	}
	path := "/api/libraries/" + url.PathEscape(libID) + "/items?limit=0&minified=1&sort=media.metadata.title"
	if err := c.do(ctx, "GET", path, nil, &r); err != nil {
		return nil, err
	}
	for i := range r.Results {
		r.Results[i].Title = r.Results[i].Media.Metadata.Title
		r.Results[i].Author = r.Results[i].Media.Metadata.Author
	}
	return r.Results, nil
}

// ---- podcast episodes ------------------------------------------------------

type Episode struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	PublishedAt *int64  `json:"publishedAt"` // ms since epoch, may be null
	AddedAt     int64   `json:"addedAt"`
	Duration    float64 `json:"duration"`
	AudioFile   *struct {
		Duration float64 `json:"duration"`
	} `json:"audioFile"`
}

// Published is the publish date, falling back to when it was added.
func (e Episode) Published() time.Time {
	if e.PublishedAt != nil && *e.PublishedAt > 0 {
		return time.UnixMilli(*e.PublishedAt)
	}
	return time.UnixMilli(e.AddedAt)
}

// Seconds returns the episode's length in seconds. It prefers the episode's own
// duration, falls back to the audio file's, and returns 0 when neither is known.
func (e Episode) Seconds() float64 {
	if e.Duration > 0 {
		return e.Duration
	}
	if e.AudioFile != nil {
		return e.AudioFile.Duration
	}
	return 0
}

type PodcastDetail struct {
	ID       string
	Title    string
	Episodes []Episode
}

// PodcastDetail fetches one podcast together with all of its episodes. The
// expanded=1 query parameter is what makes the server include the episode list.
func (c *Client) PodcastDetail(ctx context.Context, itemID string) (*PodcastDetail, error) {
	var r struct {
		ID    string `json:"id"`
		Media struct {
			Metadata struct {
				Title string `json:"title"`
			} `json:"metadata"`
			Episodes []Episode `json:"episodes"`
		} `json:"media"`
	}
	if err := c.do(ctx, "GET", "/api/items/"+url.PathEscape(itemID)+"?expanded=1", nil, &r); err != nil {
		return nil, err
	}
	return &PodcastDetail{ID: r.ID, Title: r.Media.Metadata.Title, Episodes: r.Media.Episodes}, nil
}

// ---- playlists -------------------------------------------------------------

type PlaylistItem struct {
	LibraryItemID string `json:"libraryItemId"`
	EpisodeID     string `json:"episodeId,omitempty"`
}

type Playlist struct {
	ID          string         `json:"id"`
	LibraryID   string         `json:"libraryId"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Items       []PlaylistItem `json:"items"`
}

// UnmarshalJSON accepts both the minimal {libraryItemId, episodeId} shape and
// the expanded shape ({libraryItem:{id}, episode:{id}}) servers may return.
func (it *PlaylistItem) UnmarshalJSON(b []byte) error {
	var raw struct {
		LibraryItemID string `json:"libraryItemId"`
		EpisodeID     string `json:"episodeId"`
		LibraryItem   *struct {
			ID string `json:"id"`
		} `json:"libraryItem"`
		Episode *struct {
			ID string `json:"id"`
		} `json:"episode"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	it.LibraryItemID, it.EpisodeID = raw.LibraryItemID, raw.EpisodeID
	if it.LibraryItemID == "" && raw.LibraryItem != nil {
		it.LibraryItemID = raw.LibraryItem.ID
	}
	if it.EpisodeID == "" && raw.Episode != nil {
		it.EpisodeID = raw.Episode.ID
	}
	return nil
}

// Playlists returns the current user's playlists across all libraries.
//
// Depending on the server version the list arrives under "playlists" or under
// "results", so both are accepted. The list endpoint may omit or abbreviate the
// items of each playlist; use GetPlaylist when the full ordered contents matter.
func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	var r struct {
		Playlists []Playlist `json:"playlists"`
		Results   []Playlist `json:"results"` // newer servers
	}
	if err := c.do(ctx, "GET", "/api/playlists", nil, &r); err != nil {
		return nil, err
	}
	return append(r.Playlists, r.Results...), nil
}

// GetPlaylist fetches one playlist including its full, ordered item list.
func (c *Client) GetPlaylist(ctx context.Context, id string) (*Playlist, error) {
	var p Playlist
	if err := c.do(ctx, "GET", "/api/playlists/"+url.PathEscape(id), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// AddItems appends items (in the given order) to the end of a playlist and skips
// any already present.
//
// Caveat: the server numbers each new item "current count + 1". After earlier
// removals have left gaps in the numbering, new items can therefore land in the
// middle of the playlist. Follow up with UpdatePlaylist to restore the order.
func (c *Client) AddItems(ctx context.Context, id string, items []PlaylistItem) error {
	return c.do(ctx, "POST", "/api/playlists/"+url.PathEscape(id)+"/batch/add",
		map[string]any{"items": items}, nil)
}

// CreatePlaylist creates a playlist in the given library containing items in the
// given order. The server numbers the items 1..n on creation, so the order is
// exact.
func (c *Client) CreatePlaylist(ctx context.Context, libID, name, description string, items []PlaylistItem) (*Playlist, error) {
	var p Playlist
	err := c.do(ctx, "POST", "/api/playlists", map[string]any{
		"libraryId": libID, "name": name, "description": description, "items": items,
	}, &p)
	return &p, err
}

// UpdatePlaylist PATCHes name/description. If items is non-nil it must contain
// exactly the episodes already in the playlist (the server returns 400
// otherwise); the playlist is then put into that order and renumbered 1..n.
// Note the server ignores an empty description, so it cannot be cleared.
func (c *Client) UpdatePlaylist(ctx context.Context, id, name, description string, items []PlaylistItem) error {
	body := map[string]any{"name": name, "description": description}
	if items != nil {
		body["items"] = items
	}
	return c.do(ctx, "PATCH", "/api/playlists/"+url.PathEscape(id), body, nil)
}

// RemoveItems removes the given items from a playlist. The server deletes the
// whole playlist when its last item goes, and it does not renumber the items
// that remain (so the numbering can end up with gaps).
func (c *Client) RemoveItems(ctx context.Context, id string, items []PlaylistItem) error {
	return c.do(ctx, "POST", "/api/playlists/"+url.PathEscape(id)+"/batch/remove",
		map[string]any{"items": items}, nil)
}

// DeletePlaylist deletes a playlist from the server.
func (c *Client) DeletePlaylist(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/api/playlists/"+url.PathEscape(id), nil, nil)
}
