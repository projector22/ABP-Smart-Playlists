package cron

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/projector22/abp/internal/config"
)

// TestValidate checks that well-formed cron expressions are accepted and that a
// range of malformed ones (wrong field count, out-of-range values, zero steps,
// backwards ranges, unsupported macros) are rejected.
func TestValidate(t *testing.T) {
	good := []string{"*/15 * * * *", "0 6 * * 1-5", "0 0 1,15 jan-mar *", "@daily", "30 4 * * mon", "0 */3 * * *"}
	bad := []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "5-1 * * * *", "@reboot", "a b c d e", "* * 0 * *"}
	for _, g := range good {
		if err := Validate(g); err != nil {
			t.Errorf("%q should be valid: %v", g, err)
		}
	}
	for _, b := range bad {
		if err := Validate(b); err == nil {
			t.Errorf("%q should be invalid", b)
		}
	}
}

// TestDescribe checks that preset expressions get a friendly label, ignoring
// irregular whitespace.
func TestDescribe(t *testing.T) {
	if got := Describe("*/30  * * * *"); got != "every 30 minutes" {
		t.Errorf("got %q", got)
	}
}

// TestSyncWritesScheduledEntries runs Sync against a fake `crontab` command (a
// small shell script backed by a file) and checks that each scheduled playlist
// gets a line marked -scheduled, unscheduled playlists get none, and unrelated
// crontab lines are left alone.
func TestSyncWritesScheduledEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "crontab.txt")
	script := "#!/bin/sh\nF='" + store + "'\n" +
		"case \"$1\" in\n -l) [ -s \"$F\" ] && cat \"$F\" || { echo 'no crontab for user' >&2; exit 1; } ;;\n" +
		" -r) rm -f \"$F\" ;;\n -) cat > \"$F\" ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "crontab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ABP_DATA_DIR", t.TempDir())
	if err := os.WriteFile(store, []byte("0 3 * * * /usr/bin/backup.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Playlists: []config.Playlist{
		{ID: "hist", Schedule: "*/30 * * * *"},
		{ID: "manual"},
	}}
	if err := Sync(cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(store)
	text := string(b)
	if !strings.Contains(text, "/usr/bin/backup.sh") {
		t.Errorf("unrelated line was lost:\n%s", text)
	}
	if !strings.Contains(text, "sync -scheduled hist") || !strings.Contains(text, "# abp:hist") {
		t.Errorf("scheduled playlist missing its -scheduled entry:\n%s", text)
	}
	if strings.Contains(text, "manual") {
		t.Errorf("unscheduled playlist must have no entry:\n%s", text)
	}
	m, err := Managed()
	if err != nil || m["hist"] != "*/30 * * * *" {
		t.Errorf("Managed() should still read the schedule back: %v %v", m, err)
	}
}
