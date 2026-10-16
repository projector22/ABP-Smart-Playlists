package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#5B3FD9", Dark: "#A78BFA"}
	cText   = lipgloss.AdaptiveColor{Light: "#1F1F2E", Dark: "#E6E6F0"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#7A7A8C", Dark: "#7C7C91"}
	cOK     = lipgloss.AdaptiveColor{Light: "#1E8E3E", Dark: "#5FD38D"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#B26A00", Dark: "#F5C26B"}
	cErr    = lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF7B7B"}

	sBadge   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#7C5CFA")).Padding(0, 1)
	sCrumb   = lipgloss.NewStyle().Bold(true).Foreground(cText)
	sTitle   = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sText    = lipgloss.NewStyle().Foreground(cText)
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sOK      = lipgloss.NewStyle().Foreground(cOK)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn)
	sErr     = lipgloss.NewStyle().Foreground(cErr)
	sKey     = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sSel     = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sColHdr  = lipgloss.NewStyle().Foreground(cMuted).Bold(true)
	sBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 2)
	sBoxWarn = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cWarn).Padding(0, 2)
)

// helpLine renders "key desc · key desc" from alternating pairs.
func helpLine(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sMuted.Render(pairs[i+1]))
	}
	return strings.Join(parts, sMuted.Render("  ·  "))
}

// cell truncates/pads s to exactly n display columns.
func cell(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = runewidth.Truncate(s, n, "…")
	return s + strings.Repeat(" ", n-runewidth.StringWidth(s))
}

// trunc shortens s to at most n display columns, ending with an ellipsis if it
// had to cut. Width is measured in terminal cells so wide characters are safe.
func trunc(s string, n int) string { return runewidth.Truncate(s, n, "…") }

// rule returns a muted horizontal line w columns wide.
func rule(w int) string {
	if w < 0 {
		w = 0
	}
	return sMuted.Render(strings.Repeat("─", w))
}

// ago renders a time as a short relative description ("5m ago", "3h ago",
// "2d ago"), or "never" for the zero time.
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// fmtDuration renders seconds as "42m" or "1h05m", and "—" when unknown.
func fmtDuration(sec float64) string {
	if sec <= 0 {
		return "—"
	}
	m := int(sec / 60)
	if m >= 60 {
		return fmt.Sprintf("%dh%02dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// window returns the [start,end) slice of n rows that keeps cur visible in size rows.
func window(cur, n, size int) (int, int) {
	if size < 1 {
		size = 1
	}
	if n <= size {
		return 0, n
	}
	start := cur - size/2
	if start < 0 {
		start = 0
	}
	if start+size > n {
		start = n - size
	}
	return start, start + size
}

// clamp limits v to [lo, hi]. If the range is empty (hi < lo, e.g. an empty list
// has hi = -1) it returns lo so callers need not special-case empty lists.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// pluralize renders a count with the right noun form: "1 podcast", "3 podcasts".
func pluralize(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// visibleWidth returns the on-screen width of s, ignoring colour escape codes.
func visibleWidth(s string) int { return lipgloss.Width(s) }
