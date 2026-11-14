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
// Deviation from the plan's §15 widths, per review: the harness column shows
// short names (claude/codex/opencode, 8 cells), the project column shows the
// path relative to the scope root (12 cells, hidden when every row shares it)
// and the branch column is 12 cells.
const (
	ageCol     = 6
	harnessCol = 8
	projCol    = 12
	branchCol  = 12
	colGap     = 2
	minTitle   = 8
)

func colGapStr() string { return strings.Repeat(" ", colGap) }

// pickLabelMargin reserves room for the huh selector cursor and the theme's
// base padding, so labels never wrap inside the viewport.
const pickLabelMargin = 4

// maxPickHeight is the tallest option viewport the picker shows (§15).
const maxPickHeight = 15

// Pick shows the session list in a huh select with filtering on. Esc and
// Ctrl-C return ErrCancelled (exit 130), an empty list ErrNoSessions (exit
// 3). The caller redacts the metas it wants displayed; all says whether the
// list spans every project (--all), which the header reflects.
func (u *UI) Pick(ctx context.Context, metas []session.Meta, root string, all bool, now time.Time) (session.Meta, error) {
	if len(metas) == 0 {
		return session.Meta{}, fmt.Errorf("no sessions for %s; try --all: %w", root, ErrNoSessions)
	}
	layout := newPickLayout(metas, homeDir(), root, u.width-pickLabelMargin)
	options := make([]huh.Option[session.Meta], len(metas))
	for i, m := range metas {
		options[i] = huh.NewOption(layout.row(m, now), m)
	}
	var picked session.Meta
	sel := huh.NewSelect[session.Meta]().
		Options(options...).
		Value(&picked).
		Filtering(true).
		// With Filtering(true) the title never renders; the context lives in
		// the description, which does.
		Description(u.pickDescription(root, all, len(metas), layout)).
		Height(min(maxPickHeight, len(metas)) + 2)
	sel.WithWidth(u.width)
	if err := u.runForm(ctx, huh.NewGroup(sel)); err != nil {
		return session.Meta{}, err
	}
	return picked, nil
}

// pickDescription renders the two header lines: the scope and key bindings,
// then the column header.
func (u *UI) pickDescription(root string, all bool, n int, l pickLayout) string {
	scope := RelHome(root, homeDir())
	if all {
		scope = "all projects"
	}
	line1 := fmt.Sprintf("%s · %d sessions, newest first · type to filter · ↑↓ move · enter select · esc cancel", scope, n)
	return line1 + "\n" + l.header()
}

// pickLayout is the resolved column layout of the picker rows.
type pickLayout struct {
	home, root  string
	showProject bool
	titleWidth  int
}

func newPickLayout(metas []session.Meta, home, root string, width int) pickLayout {
	showProject := len(metas) > 0 && !allEqual(metas, func(m session.Meta) string {
		return projectCell(m.Cwd, root, home)
	})
	// Gaps sit between the cells: two columns share one gap each.
	fixed := ageCol + harnessCol + branchCol + 3*colGap
	if showProject {
		fixed += projCol + colGap
	}
	return pickLayout{
		home:        home,
		root:        root,
		showProject: showProject,
		titleWidth:  max(width-fixed, minTitle),
	}
}

func (l pickLayout) header() string {
	cells := []string{
		padRight("age", ageCol),
		padRight("harness", harnessCol),
	}
	if l.showProject {
		cells = append(cells, padRight("project", projCol))
	}
	cells = append(cells, padRight("branch", branchCol), "title")
	return strings.Join(cells, colGapStr())
}

func (l pickLayout) rows(metas []session.Meta, now time.Time) []string {
	rows := make([]string, len(metas))
	for i, m := range metas {
		rows[i] = l.row(m, now)
	}
	return rows
}

func (l pickLayout) row(m session.Meta, now time.Time) string {
	cells := []string{
		padRight(FormatAge(m.UpdatedAt, now), ageCol),
		padRight(truncCells(displayHarness(m.Harness), harnessCol), harnessCol),
	}
	if l.showProject {
		cells = append(cells, padRight(projectCell(m.Cwd, l.root, l.home), projCol))
	}
	cells = append(cells,
		padRight(truncCells(m.Branch, branchCol), branchCol),
		truncCells(m.Title, l.titleWidth))
	return strings.Join(cells, colGapStr())
}

// projectCell renders a session cwd relative to the scope root, truncated
// with its last path segment kept whole. A session in the root itself shows
// the ~-folded root instead of a bare ".".
func projectCell(cwd, root, home string) string {
	if cwd == "" {
		return "-"
	}
	return middleTruncatePath(displayPath(relScope(cwd, root, home)), projCol)
}

// displayPath renders a path label with "/" separators on every OS. Labels
// feed the picker's filter, and "/" keeps filtering and truncation
// consistent across machines.
func displayPath(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// relScope expresses cwd relative to root when it sits under it, else
// relative to home.
func relScope(cwd, root, home string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, cwd); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return RelHome(cwd, home)
			}
			return rel
		}
	}
	return RelHome(cwd, home)
}

func allEqual(metas []session.Meta, key func(session.Meta) string) bool {
	if len(metas) == 0 {
		return true
	}
	first := key(metas[0])
	for _, m := range metas[1:] {
		if key(m) != first {
			return false
		}
	}
	return true
}

func displayHarness(h session.Harness) string {
	switch h {
	case session.HarnessClaudeCode:
		return "claude"
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

// RelHome shortens an absolute path under home to "~/…". The label is
// OS-independent: "/" separates the segments on every platform, so the
// picker's filter and middle truncation see the same characters everywhere.
func RelHome(p, home string) string {
	if p == "" {
		return "-"
	}
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if !strings.HasPrefix(p, home) {
		return p
	}
	rest := p[len(home):]
	if rest == "" || (rest[0] != '/' && rest[0] != '\\') {
		return p
	}
	if rest == "/" || rest == `\` {
		return "~"
	}
	return "~/" + rest[1:]
}

// FormatMatches renders ambiguous-match table rows in the picker's column
// style: age, harness, id, branch, title. The caller truncates by passing
// width and applies redaction/CleanTitle itself if needed.
func FormatMatches(metas []session.Meta, now time.Time, width int) []string {
	const idCol = 28
	fixed := ageCol + harnessCol + idCol + branchCol + 4*colGap
	title := max(width-fixed, minTitle)
	rows := make([]string, len(metas))
	for i, m := range metas {
		rows[i] = strings.Join([]string{
			padRight(FormatAge(m.UpdatedAt, now), ageCol),
			padRight(truncCells(displayHarness(m.Harness), harnessCol), harnessCol),
			padRight(truncCells(m.ID, idCol), idCol),
			padRight(truncCells(m.Branch, branchCol), branchCol),
			truncCells(m.Title, title),
		}, colGapStr())
	}
	return rows
}
