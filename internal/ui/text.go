package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The label helpers measure and cut by display cells (lipgloss.Width), not
// runes, so wide characters cannot overflow a column.

// truncCells cuts s to max display cells, replacing the cut with an ellipsis.
func truncCells(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// middleCells middle-truncates s to max display cells: a head and a tail with
// an ellipsis between them. When one side cannot use its share (wide runes),
// the other side takes the leftover.
func middleCells(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	budget := max - 1
	head := widestPrefix(s, budget/2)
	tail := widestSuffix(s, budget-lipgloss.Width(head))
	if spare := budget - lipgloss.Width(head) - lipgloss.Width(tail); spare > 0 {
		head = widestPrefix(s, lipgloss.Width(head)+spare)
	}
	return head + "…" + tail
}

// middleTruncatePath middle-truncates a path while keeping its last segment
// whole: ~/Projects/very/deep/api → ~/Proje…/api, and a long tail collapses
// the head to "~/…".
func middleTruncatePath(p string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(p) <= max {
		return p
	}
	if i := strings.LastIndex(p, "/"); i > 0 {
		head, last := p[:i], p[i:] // last keeps its leading slash
		if avail := max - lipgloss.Width(last); avail >= 1 {
			return truncCells(head, avail) + last
		}
	}
	return middleCells(p, max)
}

// widestPrefix returns the longest prefix of s of at most max cells.
func widestPrefix(s string, max int) string {
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// widestSuffix returns the longest suffix of s of at most max cells.
func widestSuffix(s string, max int) string {
	r := []rune(s)
	w := 0
	start := len(r)
	for start > 0 {
		rw := lipgloss.Width(string(r[start-1]))
		if w+rw > max {
			break
		}
		start--
		w += rw
	}
	return string(r[start:])
}

// padRight pads s with spaces to width display cells.
func padRight(s string, width int) string {
	if n := lipgloss.Width(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}
