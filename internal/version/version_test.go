package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestParse checks that versions in every shape abp can meet are read
// leniently: plain tags, git-describe output and Go pseudo-versions.
func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want semver
	}{
		{"v0.1.0", true, semver{0, 1, 0, ""}},
		{"1.2.3", true, semver{1, 2, 3, ""}},
		{"v1.2.3-4-gabc1234-dirty", true, semver{1, 2, 3, "-4-gabc1234-dirty"}},
		{"v0.1.1-0.20261017123456-abcdef123456", true, semver{0, 1, 1, "-0.20261017123456-abcdef123456"}},
		{"v0.1.0+dirty", true, semver{0, 1, 0, "+dirty"}},
		{"dev", false, semver{}},
		{"", false, semver{}},
		{"v1.2", false, semver{}},
	}
	for _, c := range cases {
		got, ok := parse(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parse(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestClassify covers the user-visible outcomes: release vs development build,
// older vs newer than the latest release.
func TestClassify(t *testing.T) {
	cases := []struct {
		build  string
		latest string
		want   Status
	}{
		{"v0.1.0", "v0.1.0", UpToDate},
		{"v0.1.0", "v0.2.0", UpdateAvailable},
		{"v0.9.0", "v0.10.0", UpdateAvailable}, // numeric, not string, comparison
		{"v0.2.0", "v0.1.0", Ahead},            // tagged but unreleased
		{"v0.1.0-3-gabc1234", "v0.1.0", Ahead}, // commits after the tag
		{"v0.1.0-dirty", "v0.1.0", Ahead},      // uncommitted changes
		{"v0.1.1-0.20261017123456-abcdef123456", "v0.1.0", Ahead},
		{"v0.0.0-20261017123456-abcdef123456", "v0.1.0", UpdateAvailable}, // commit older than first release
		{"dev", "v0.1.0", Ahead},
		{"v0.1.0", "garbage", Unknown},
	}
	for _, c := range cases {
		b := Detect(c.build)
		if got := classify(b, c.latest); got != c.want {
			t.Errorf("classify(%q, %q) = %v, want %v", c.build, c.latest, got, c.want)
		}
	}
}

// TestDetect checks that an exact tag is a release and anything else is a
// development build, and that the labels shown in the UI follow from that.
func TestDetect(t *testing.T) {
	if b := Detect("v0.1.0"); !b.Release || b.Short() != "v0.1.0" || b.Long() != "v0.1.0" {
		t.Errorf("tagged build misdetected: %+v", b)
	}
	b := Detect("v0.1.0-3-gabc1234-dirty")
	if b.Release || b.Short() != "dev" || !strings.Contains(b.Long(), "development build") {
		t.Errorf("describe build misdetected: %+v / %q", b, b.Long())
	}
	if b := Detect(""); b.Release || b.Short() != "dev" {
		t.Errorf("empty ldflags should be a dev build: %+v", b)
	}
}

// fakeGitHub imitates GitHub's /<repo>/releases/latest page: it answers with the
// given status and, if location is non-empty, a redirect to it. It counts the
// requests it receives.
func fakeGitHub(t *testing.T, status int, location string) (*atomic.Int32, func()) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub requires a User-Agent header")
		}
		if r.URL.Path != "/"+Repo+"/releases/latest" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
	}))
	old := webBase
	webBase = srv.URL
	return &hits, func() { webBase = old; srv.Close() }
}

// latestV2 is what GitHub answers when v0.2.0 is the newest release (a relative
// redirect, as the real site sends).
const latestV2 = "/projector22/abp/releases/tag/v0.2.0"

// TestCheckStatuses runs the whole check against a fake GitHub for each answer
// a user can get.
func TestCheckStatuses(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	_, done := fakeGitHub(t, 302, latestV2)
	defer done()
	ctx := context.Background()

	res := Check(ctx, Detect("v0.1.0"), 0)
	if res.Status != UpdateAvailable || res.Latest != "v0.2.0" || !strings.Contains(strings.Join(res.Lines(), "\n"), "Update available") {
		t.Fatalf("update: %+v", res)
	}
	if !strings.Contains(res.Summary(), "update available") {
		t.Errorf("summary: %q", res.Summary())
	}
	if res = Check(ctx, Detect("v0.2.0"), 0); res.Status != UpToDate {
		t.Fatalf("up to date: %+v", res)
	}
	if res = Check(ctx, Detect("v0.2.0-5-gabc1234"), 0); res.Status != Ahead || !strings.Contains(res.Summary(), "development build") {
		t.Fatalf("ahead: %+v / %q", res, res.Summary())
	}
}

// TestCheckNoReleasesYet is the situation before the first tag is pushed: GitHub
// redirects /releases/latest to the (empty) releases list.
func TestCheckNoReleasesYet(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	_, done := fakeGitHub(t, 302, "/projector22/abp/releases")
	defer done()
	res := Check(context.Background(), Detect("dev"), 0)
	if !res.NoReleases || res.Err != nil || res.Status != Unknown {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(strings.Join(res.Lines(), " "), "development build") {
		t.Errorf("lines: %v", res.Lines())
	}
}

// TestCheckCacheAndFallback checks the cache is used within its lifetime, is
// bypassed with maxAge 0, and still answers when GitHub is unreachable.
func TestCheckCacheAndFallback(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	hits, done := fakeGitHub(t, 302, latestV2)
	ctx := context.Background()
	b := Detect("v0.1.0")

	Check(ctx, b, CacheTTL)
	res := Check(ctx, b, CacheTTL)
	if hits.Load() != 1 || !res.Cached || res.Status != UpdateAvailable {
		t.Fatalf("second call should come from the cache: hits=%d %+v", hits.Load(), res)
	}
	Check(ctx, b, 0)
	if hits.Load() != 2 {
		t.Fatalf("maxAge 0 must always ask GitHub, hits=%d", hits.Load())
	}

	done() // GitHub is now unreachable
	webBase = "http://127.0.0.1:1"
	defer func() { webBase = "https://github.com" }()
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	res = Check(ctx2, b, 0)
	if res.Err == nil || res.Status != UpdateAvailable || res.Latest != "v0.2.0" {
		t.Fatalf("should fall back to the stale cache: %+v", res)
	}
	if !strings.Contains(strings.Join(res.Lines(), "\n"), "last answer") {
		t.Errorf("should explain the stale answer: %v", res.Lines())
	}
}

// TestCheckOfflineWithoutCache is the first-ever run with no network.
func TestCheckOfflineWithoutCache(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	webBase = "http://127.0.0.1:1"
	defer func() { webBase = "https://github.com" }()
	res := Check(context.Background(), Detect("v0.1.0"), 0)
	if res.Err == nil || res.Status != Unknown || !strings.Contains(res.Summary(), "couldn't check") {
		t.Fatalf("%+v / %q", res, res.Summary())
	}
}

// TestCheckDisabled makes sure the opt-out never touches the network.
func TestCheckDisabled(t *testing.T) {
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	t.Setenv(DisableEnv, "1")
	hits, done := fakeGitHub(t, 302, latestV2)
	defer done()
	res := Check(context.Background(), Detect("v0.1.0"), 0)
	if !res.Disabled || hits.Load() != 0 {
		t.Fatalf("disabled check made %d requests: %+v", hits.Load(), res)
	}
}

// TestCheckServerProblems checks the messages for the failures a user could
// actually meet: a rate-limited network and a wrong repository.
func TestCheckServerProblems(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{429, "rate limiting"},
		{404, "was not found"},
		{500, "500"},
	}
	for _, c := range cases {
		t.Setenv("ABP_DATA_DIR", t.TempDir())
		_, done := fakeGitHub(t, c.status, "")
		res := Check(context.Background(), Detect("v0.1.0"), 0)
		done()
		if res.Err == nil || !strings.Contains(res.Err.Error(), c.want) || res.Status != Unknown {
			t.Errorf("status %d: want error containing %q, got %+v", c.status, c.want, res)
		}
	}
}
