// Package cron manages abp's entries in the user's crontab. Every line abp owns
// ends with "# abp:<playlist-id>", so anything else in the crontab is untouched.
package cron

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"abp/internal/config"
)

const tag = "# abp:"

type Preset struct {
	Label string
	Expr  string
}

var Presets = []Preset{
	{"Every 15 minutes", "*/15 * * * *"},
	{"Every 30 minutes", "*/30 * * * *"},
	{"Hourly", "0 * * * *"},
	{"Every 3 hours", "0 */3 * * *"},
	{"Every 6 hours", "0 */6 * * *"},
	{"Daily at 06:00", "0 6 * * *"},
}

// Describe returns a friendly label for known presets, else the raw expression.
func Describe(expr string) string {
	expr = strings.Join(strings.Fields(expr), " ")
	for _, p := range Presets {
		if p.Expr == expr {
			return strings.ToLower(p.Label[:1]) + p.Label[1:]
		}
	}
	return expr
}

// ---- validation ------------------------------------------------------------

var macros = map[string]bool{
	"@hourly": true, "@daily": true, "@midnight": true,
	"@weekly": true, "@monthly": true, "@yearly": true, "@annually": true,
}

type bounds struct {
	name   string
	lo, hi int
	names  map[string]int
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

var fields = []bounds{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of month", 1, 31, nil},
	{"month", 1, 12, monthNames},
	{"day of week", 0, 7, dowNames},
}

// Validate checks a 5-field cron expression (or an @macro).
func Validate(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return errors.New("schedule is empty")
	}
	if strings.HasPrefix(expr, "@") {
		if macros[expr] {
			return nil
		}
		return fmt.Errorf("unsupported macro %q", expr)
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return fmt.Errorf("need 5 fields (min hour dom month dow), got %d", len(parts))
	}
	for i, p := range parts {
		if err := validateField(p, fields[i]); err != nil {
			return fmt.Errorf("%s: %w", fields[i].name, err)
		}
	}
	return nil
}

// validateField checks one cron field against its allowed range. A field is a
// comma-separated list of parts; each part is "*", a single value or an "a-b"
// range, optionally followed by "/step". Month and weekday names are accepted
// where the bounds define them.
func validateField(f string, b bounds) error {
	for _, part := range strings.Split(f, ",") {
		rng := part
		if i := strings.Index(part, "/"); i >= 0 {
			rng = part[:i]
			step, err := strconv.Atoi(part[i+1:])
			if err != nil || step < 1 {
				return fmt.Errorf("bad step in %q", part)
			}
		}
		if rng == "*" {
			continue
		}
		lo, hi, isRange := strings.Cut(rng, "-")
		a, err := parseVal(lo, b)
		if err != nil {
			return err
		}
		if isRange {
			z, err := parseVal(hi, b)
			if err != nil {
				return err
			}
			if a > z {
				return fmt.Errorf("range %q is backwards", rng)
			}
		}
	}
	return nil
}

// parseVal converts one value in a cron field (a number or a month/weekday
// name) to an int and checks that it lies within the field's bounds.
func parseVal(s string, b bounds) (int, error) {
	if b.names != nil {
		if v, ok := b.names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	if v < b.lo || v > b.hi {
		return 0, fmt.Errorf("%d out of range %d-%d", v, b.lo, b.hi)
	}
	return v, nil
}

// ---- crontab I/O -----------------------------------------------------------

// read returns the user's current crontab, one entry per line. "No crontab yet"
// is not an error and yields nil; a missing crontab command or any other failure
// is reported.
func read() ([]string, error) {
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("crontab command not found on this system")
		}
		if strings.Contains(strings.ToLower(string(out)), "no crontab") {
			return nil, nil
		}
		return nil, fmt.Errorf("crontab -l: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
}

// write replaces the user's crontab with lines. An empty list removes the
// crontab altogether (crontab -r) because some systems reject an empty file.
func write(lines []string) error {
	if len(lines) == 0 {
		// Nothing left: remove the crontab entirely (ignore "no crontab" errors).
		_ = exec.Command("crontab", "-r").Run()
		return nil
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// managedID returns the playlist id from an abp-owned crontab line (one ending in
// "# abp:<id>") and reports whether the line is one of ours.
func managedID(line string) (string, bool) {
	i := strings.LastIndex(line, tag)
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(line[i+len(tag):]), true
}

// scheduleOf extracts the schedule from the start of a crontab line: either an
// "@macro" or the first five whitespace-separated fields.
func scheduleOf(line string) string {
	f := strings.Fields(line)
	if len(f) > 0 && strings.HasPrefix(f[0], "@") {
		return f[0]
	}
	if len(f) >= 5 {
		return strings.Join(f[:5], " ")
	}
	return ""
}

// Managed returns the abp-owned schedules currently in the crontab, by playlist id.
func Managed() (map[string]string, error) {
	lines, err := read()
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range lines {
		if id, ok := managedID(l); ok {
			m[id] = scheduleOf(l)
		}
	}
	return m, nil
}

// shq quotes s for safe use in a crontab command line: single quotes for the
// shell, plus a backslash before % because cron treats a bare % as a newline.
func shq(s string) string {
	s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	return strings.ReplaceAll(s, "%", `\%`) // % is special in crontab
}

// exePath returns the absolute path of the running binary with symlinks resolved.
// Cron has a minimal PATH, so the crontab entry must use the full path.
func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "abp"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

// Sync makes the crontab match the config: one line per playlist that has a
// schedule, and no leftover abp lines for anything else.
func Sync(cfg *config.Config) error {
	var env []string
	for _, k := range []string{"ABP_CONFIG", "ABP_DATA_DIR", "XDG_DATA_HOME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+shq(v))
		}
	}
	envPrefix := ""
	if len(env) > 0 {
		envPrefix = strings.Join(env, " ") + " "
	}

	// Start from the current crontab, dropping every line abp owns so removed or
	// renamed playlists don't leave stale entries. Everyone else's lines are kept
	// as they are.
	existing, err := read()
	if err != nil {
		return err
	}
	var out []string
	for _, l := range existing {
		if _, ok := managedID(l); !ok {
			out = append(out, l)
		}
	}
	// Then add one fresh line per scheduled playlist, tagged with its id.
	for _, p := range cfg.Playlists {
		if p.Schedule == "" {
			continue
		}
		if err := Validate(p.Schedule); err != nil {
			return fmt.Errorf("playlist %q: %w", p.ID, err)
		}
		out = append(out, fmt.Sprintf("%s %s%s sync %s >> %s 2>&1 %s%s",
			p.Schedule, envPrefix, shq(exePath()), p.ID, shq(config.LogPath()), tag, p.ID))
	}
	// Avoid rewriting the crontab when nothing changed.
	if strings.Join(out, "\n") == strings.Join(existing, "\n") {
		return nil
	}
	if err := os.MkdirAll(config.DataDir(), 0o700); err != nil {
		return err
	}
	return write(out)
}

// Remove strips every abp line from the crontab.
func Remove() error { return Sync(&config.Config{}) }
