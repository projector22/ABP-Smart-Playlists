// Package version works out which abp build is running and whether a newer
// release exists on GitHub.
//
// A build is either a clean tagged release (e.g. "v0.1.0", built by the
// release workflow) or a development build: anything installed from an
// untagged commit with `go install`, built locally with extra commits or
// uncommitted changes, or built without any version information.
package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/projector22/abp/internal/config"
)

// Repo is the GitHub repository that releases are published to.
const Repo = "projector22/abp"

// CacheTTL is how long a fetched "latest release" answer is trusted by the TUI,
// which keeps abp well inside GitHub's unauthenticated rate limit.
const CacheTTL = 24 * time.Hour

// DisableEnv, when set to any value, turns the update check off entirely.
const DisableEnv = "ABP_NO_UPDATE_CHECK"

// webBase is the GitHub website root. It is a variable so tests can point it at
// a local server.
var webBase = "https://github.com"

// errNoReleases means the repository exists but has no published release yet.
var errNoReleases = errors.New("no releases published yet")

// Status says how the running build relates to the latest release.
type Status int

const (
	// Unknown means the comparison could not be made (offline, check disabled,
	// no releases yet, or an unparseable version).
	Unknown Status = iota
	// UpToDate means this is the latest tagged release.
	UpToDate
	// UpdateAvailable means a newer release than this build exists.
	UpdateAvailable
	// Ahead means this build is newer than the latest release: a development
	// build.
	Ahead
)

// ---- build detection -------------------------------------------------------

// Build describes the running binary.
type Build struct {
	Version string // "v0.1.0", a git-describe or pseudo version, or "dev"
	Commit  string // short VCS revision when the toolchain recorded one
	Dirty   bool   // built from a tree with uncommitted changes
	Release bool   // a clean tagged release, exactly vMAJOR.MINOR.PATCH

	base    semver // the vMAJOR.MINOR.PATCH at the start of Version
	hasBase bool
}

// Detect builds a Build from the version stamped in with -ldflags (which may be
// empty or "dev") and from the build information Go embeds in every binary.
//
// A release build carries its tag via -ldflags. A `go install` build has no
// ldflags, so the module version Go recorded is used instead: a real tag when
// installed from one, or a pseudo-version when installed from a commit.
func Detect(ldflags string) Build {
	v := strings.TrimSpace(ldflags)
	var b Build

	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" || v == "dev" {
			if mv := info.Main.Version; mv != "" && mv != "(devel)" {
				v = mv
			}
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if len(s.Value) > 7 {
					b.Commit = s.Value[:7]
				} else {
					b.Commit = s.Value
				}
			case "vcs.modified":
				b.Dirty = s.Value == "true"
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	b.Version = v
	if sv, ok := parse(v); ok {
		b.base, b.hasBase = sv, true
		b.Release = sv.rest == "" // anything after the patch number means "not exactly the tag"
	}
	return b
}

// Short is the compact label for the UI: the tag for a release, else "dev".
func (b Build) Short() string {
	if b.Release {
		return b.Version
	}
	return "dev"
}

// Long is the full description printed by `abp -v`.
func (b Build) Long() string {
	if b.Release {
		return b.Version
	}
	s := b.Version
	var extra []string
	if b.Commit != "" && !strings.Contains(b.Version, b.Commit) {
		extra = append(extra, "commit "+b.Commit)
	}
	if b.Dirty {
		extra = append(extra, "uncommitted changes")
	}
	extra = append(extra, "development build")
	return s + " (" + strings.Join(extra, ", ") + ")"
}

// ---- version comparison ----------------------------------------------------

// semver is the MAJOR.MINOR.PATCH core of a version, plus whatever follows it.
type semver struct {
	major, minor, patch int
	rest                string
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(.*)$`)

// parse reads a version such as "v1.2.3", "v1.2.3-4-gabc1234-dirty" or
// "v0.1.1-0.20261017123456-abcdef123456". It is deliberately lenient: it only
// needs the leading MAJOR.MINOR.PATCH, and keeps the remainder in rest.
func parse(v string) (semver, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return semver{}, false
	}
	n := func(s string) int { i, _ := strconv.Atoi(s); return i }
	return semver{n(m[1]), n(m[2]), n(m[3]), m[4]}, true
}

// cmp compares only the MAJOR.MINOR.PATCH cores: -1, 0 or 1.
func cmp(a, b semver) int {
	for _, p := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// classify compares a build with the latest release tag.
//
// A build older than the latest release has an update available, whether it is
// a release or a development build. A build on the same version is up to date
// only if it is exactly the tagged release; a development build on that version
// (extra commits, local changes) is ahead of it. A newer version is ahead too.
func classify(b Build, latestTag string) Status {
	latest, ok := parse(latestTag)
	if !ok {
		return Unknown
	}
	if !b.hasBase {
		return Ahead // "dev": no version information at all
	}
	switch c := cmp(b.base, latest); {
	case c < 0:
		return UpdateAvailable
	case c == 0 && b.Release:
		return UpToDate
	default:
		return Ahead
	}
}

// ---- checking GitHub -------------------------------------------------------

// Result is the outcome of an update check.
type Result struct {
	Build      Build
	Status     Status
	Latest     string // latest release tag, e.g. "v0.2.0" (empty if unknown)
	URL        string // that release's web page
	NoReleases bool   // the repo has no published release yet
	Disabled   bool   // the check was switched off with ABP_NO_UPDATE_CHECK
	Cached     bool   // Latest came from the cache without contacting GitHub
	Err        error  // the live check failed (Latest may still come from a stale cache)
}

// cacheFile is what is remembered between runs.
type cacheFile struct {
	CheckedAt  time.Time `json:"checked_at"`
	Tag        string    `json:"tag"`
	URL        string    `json:"url"`
	NoReleases bool      `json:"no_releases,omitempty"`
}

// cachePath is where the last update-check answer is remembered, inside the
// private data directory.
func cachePath() string { return filepath.Join(config.DataDir(), "update-check.json") }

// loadCache reads the remembered answer, if there is one.
func loadCache() (cacheFile, bool) {
	var c cacheFile
	b, err := os.ReadFile(cachePath())
	if err != nil || json.Unmarshal(b, &c) != nil || c.CheckedAt.IsZero() {
		return cacheFile{}, false
	}
	return c, true
}

// saveCache remembers an answer. Failure is ignored: it is only an optimisation.
func saveCache(c cacheFile) {
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cachePath()), 0o700)
	_ = os.WriteFile(cachePath(), b, 0o600)
}

// fetchLatest asks GitHub for the newest published (non-draft, non-prerelease)
// release.
//
// It deliberately does not use the REST API: that allows only 60 anonymous
// requests an hour per IP address, which people on shared networks (offices,
// CGNAT) use up before abp gets a turn. Instead it requests the public
// ".../releases/latest" page, which GitHub answers with a redirect to
// ".../releases/tag/<tag>" and which has no such limit. The redirect is read,
// not followed. If the repository has no releases, GitHub redirects somewhere
// else (the releases list), which is reported as errNoReleases.
func fetchLatest(ctx context.Context, b Build) (tag, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webBase+"/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "abp/"+b.Short())
	client := &http.Client{
		// Stop at the first response so we can read where it points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc, err := resp.Location() // resolves relative redirects against the request URL
		if err != nil {
			return "", "", err
		}
		i := strings.Index(loc.Path, "/releases/tag/")
		if i < 0 {
			return "", "", errNoReleases
		}
		tag, err := neturl.PathUnescape(loc.Path[i+len("/releases/tag/"):])
		if err != nil || tag == "" {
			return "", "", errors.New("couldn't read the release tag from GitHub's reply")
		}
		return tag, loc.String(), nil
	case resp.StatusCode == http.StatusNotFound:
		return "", "", fmt.Errorf("repository %s was not found on GitHub", Repo)
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", "", errors.New("GitHub is rate limiting requests from this network, try again later")
	default:
		return "", "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
}

// Check finds the latest release and compares it with b.
//
// An answer younger than maxAge is taken from the cache without any network
// traffic; pass 0 to always ask GitHub. If asking fails, an older cached answer
// is still used so the UI can keep showing a known update. Setting
// ABP_NO_UPDATE_CHECK disables the check completely.
func Check(ctx context.Context, b Build, maxAge time.Duration) Result {
	res := Result{Build: b}
	if os.Getenv(DisableEnv) != "" {
		res.Disabled = true
		return res
	}

	c, have := loadCache()
	if have && maxAge > 0 && time.Since(c.CheckedAt) < maxAge {
		res.Cached = true
	} else {
		tag, url, err := fetchLatest(ctx, b)
		switch {
		case err == nil:
			c, have = cacheFile{CheckedAt: time.Now(), Tag: tag, URL: url}, true
			saveCache(c)
		case errors.Is(err, errNoReleases):
			c, have = cacheFile{CheckedAt: time.Now(), NoReleases: true}, true
			saveCache(c)
		default:
			res.Err = err // fall through with whatever the cache holds
		}
	}

	if !have {
		return res
	}
	if c.NoReleases {
		res.NoReleases = true
		return res
	}
	res.Latest, res.URL = c.Tag, c.URL
	if res.URL == "" {
		res.URL = "https://github.com/" + Repo + "/releases/latest"
	}
	res.Status = classify(b, c.Tag)
	return res
}

// ---- presentation ----------------------------------------------------------

// Summary is a single line suitable for a status bar.
func (r Result) Summary() string {
	name := "abp " + r.Build.Short()
	switch {
	case r.Disabled:
		return name + " (update check disabled)"
	case r.Status == UpdateAvailable:
		return fmt.Sprintf("%s: update available, %s (go install github.com/%s@latest)", name, r.Latest, Repo)
	case r.Status == UpToDate:
		return name + ": up to date"
	case r.Status == Ahead && r.Latest != "":
		return fmt.Sprintf("%s: development build, ahead of the latest release (%s)", name, r.Latest)
	case r.Status == Ahead:
		return name + ": development build"
	case r.NoReleases && !r.Build.Release:
		return name + ": development build (no releases published yet)"
	case r.NoReleases:
		return name + " (no releases published yet)"
	case r.Err != nil:
		return name + ": couldn't check for updates (" + r.Err.Error() + ")"
	}
	return name
}

// Lines is the longer explanation printed by `abp -v`, one line per entry.
func (r Result) Lines() []string {
	var out []string
	switch {
	case r.Disabled:
		out = append(out, "Update check is disabled ("+DisableEnv+" is set).")
	case r.Status == UpdateAvailable:
		out = append(out,
			fmt.Sprintf("Update available: %s (you have %s).", r.Latest, r.Build.Version),
			"Release notes and downloads: "+r.URL,
			"Update with: go install github.com/"+Repo+"@latest")
	case r.Status == UpToDate:
		out = append(out, fmt.Sprintf("You're up to date (latest release: %s).", r.Latest))
	case r.Status == Ahead && r.Latest != "":
		out = append(out, fmt.Sprintf("You're running a development build, ahead of the latest release (%s).", r.Latest))
	case r.Status == Ahead:
		out = append(out, "You're running a development build.")
	case r.NoReleases:
		out = append(out, "No releases have been published yet.")
		if !r.Build.Release {
			out = append(out, "You're running a development build.")
		}
	case r.Err != nil:
		out = append(out, "Couldn't check for updates: "+r.Err.Error())
	}
	if r.Err != nil && r.Latest != "" {
		out = append(out, "(Couldn't reach GitHub just now, so this uses the last answer it gave: "+r.Err.Error()+")")
	}
	return out
}
