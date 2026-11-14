package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// Column widths of the picker label; the title gets whatever is left.
const (
	ageCol     = 6
	harnessCol = 11
	projectCol = 24
	branchCol  = 14
	colGap     = 2
	minTitle   = 8
)

// pickLabelMargin reserves room for the huh selector cursor and the theme's
// base padding, so labels never wrap inside the viewport.
const pickLabelMargin = 4

// maxPickHeight is the tallest option viewport the picker shows (§15).
const maxPickHeight = 15

// Pick shows the session list in a huh select with filtering on. Esc and
// Ctrl-C return ErrCancelled (exit 130), an empty list ErrNoSessions
// (exit 3). The caller redacts the metas it wants displayed.
func (u *UI) Pick(ctx context.Context, metas []session.Meta, root string, now time.Time) (session.Meta, error) {
	if len(metas) == 0 {
		return session.Meta{}, fmt.Errorf("no sessions for %s; try --all: %w", root, ErrNoSessions)
	}
	home := homeDir()
	rows := FormatRows(metas, now, home, u.width-pickLabelMargin)
	options := make([]huh.Option[session.Meta], len(metas))
	for i, m := range metas {
		options[i] = huh.NewOption(rows[i], m)
	}
	var picked session.Meta
	sel := huh.NewSelect[session.Meta]().
		Title(fmt.Sprintf("sessions for %s — %d, newest first", root, len(metas))).
		Options(options...).
		Value(&picked).
		Filtering(true).
		Height(min(maxPickHeight, len(metas)) + 1)
	sel.WithWidth(u.width)
	if err := u.runForm(ctx, huh.NewGroup(sel)); err != nil {
		return session.Meta{}, err
	}
	return picked, nil
}

// FormatRows renders the picker option labels:
//
//	<age 6>  <harness 11>  <project 24>  <branch 14>  <title…>
//
// truncated to width.
func FormatRows(metas []session.Meta, now time.Time, home string, width int) []string {
	title := width - (ageCol + harnessCol + projectCol + branchCol + 4*colGap)
	title = max(title, minTitle)
	rows := make([]string, len(metas))
	for i, m := range metas {
		rows[i] = FormatRow(m, now, home, title)
	}
	return rows
}

// FormatRow renders one option label with fixed-width columns.
func FormatRow(m session.Meta, now time.Time, home string, titleWidth int) string {
	age := padRight(FormatAge(m.UpdatedAt, now), ageCol)
	name := padRight(truncRight(displayHarness(m.Harness), harnessCol), harnessCol)
	proj := padRight(middleTruncate(relHome(m.Cwd, home), projectCol), projectCol)
	branch := padRight(truncRight(m.Branch, branchCol), branchCol)
	title := truncRight(m.Title, titleWidth)
	if title == "" {
		title = "-"
	}
	return strings.Join([]string{age, name, proj, branch, title}, strings.Repeat(" ", colGap))
}

func displayHarness(h session.Harness) string {
	switch h {
	case session.HarnessClaudeCode:
		return "claude-code"
	default:
		return string(h)
	}
}

// FormatAge renders a session's age as 5m, 3h, 2d or, for older ones,
// "Nov 02".
func FormatAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero() || d < 0:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", max(1, int(d.Hours())))
	case d < 10*24*time.Hour:
		return fmt.Sprintf("%dd", max(1, int(d.Hours()/24)))
	default:
		return t.Format("Jan 02")
	}
}

// relHome shortens an absolute path under home to "~/…".
func relHome(p, home string) string {
	if p == "" {
		return "-"
	}
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// middleTruncate cuts s to max runes keeping its head and tail with an
// ellipsis in between ("…").
func middleTruncate(s string, max int) string {
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	head := (max - 1 + 1) / 2
	tail := max - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// truncRight cuts s to max runes, replacing the last one with an ellipsis.
func truncRight(s string, max int) string {
	r := []rune(s)
	if max <= 0 {
		return ""
	}
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func padRight(s string, width int) string {
	if n := len([]rune(s)); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}
