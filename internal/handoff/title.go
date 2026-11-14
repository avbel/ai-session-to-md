package handoff

import (
	"strings"
	"unicode"
)

// CleanTitle makes a session title safe and compact for display: the first
// non-empty line, whitespace collapsed to single spaces, control characters
// (including ESC sequences) removed, capped at max runes. The cmd and ui
// packages call it at display time; session.Meta keeps the full title so
// redaction patterns can match secrets that straddle any cut.
func CleanTitle(s string, max int) string {
	for line := range strings.Lines(s) {
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		return capRunes(cleanTitleLine(line), max)
	}
	return ""
}

// cleanTitleLine collapses whitespace and drops control runes from one line.
func cleanTitleLine(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	for _, r := range line {
		if unicode.IsControl(r) {
			continue
		}
		if unicode.IsSpace(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
