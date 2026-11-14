package reduce

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const textCutMarker = "\n[…truncated…]\n"

// capHeadTail caps s at max bytes, keeping its beginning and end joined by a
// truncation marker. Cuts fall on rune boundaries.
func capHeadTail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	keep := max - len(textCutMarker)
	if keep <= 0 {
		return cutAt(s, max)
	}
	return cutAt(s, keep-keep/2) + textCutMarker + tailAt(s, keep/2)
}

// cutAt returns the first max bytes of s, ending on a rune boundary.
func cutAt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// tailAt returns the last max bytes of s, starting on a rune boundary.
func tailAt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	start := len(s) - max
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// capOpt caps s at max bytes when max is positive; 0 or less means uncapped.
func capOpt(s string, max int) string {
	if max <= 0 {
		return s
	}
	return capHeadTail(s, max)
}

// truncLines keeps the first head and last tail lines of s, at most max bytes
// in total, replacing the middle lines with the plan's marker.
func truncLines(s string, head, tail, max int) string {
	lines := splitLines(s)
	if len(lines) <= head+tail {
		if len(s) <= max {
			return s
		}
		return capHeadTail(s, max)
	}
	marker := fmt.Sprintf("… [%d lines truncated] …", len(lines)-head-tail)
	headText := capHeadTail(strings.Join(lines[:head], ""), sideBudget(max, len(marker)))
	tailText := capHeadTail(strings.Join(lines[len(lines)-tail:], ""), sideBudget(max, len(marker)))
	if headText != "" && !strings.HasSuffix(headText, "\n") {
		headText += "\n"
	}
	return headText + marker + "\n" + tailText
}

// sideBudget is the byte budget for each side of the line-truncation marker:
// half of max, minus room for the marker and the newlines around it.
func sideBudget(max, markerLen int) int {
	budget := (max - markerLen - 2) / 2
	if budget < 0 {
		return 0
	}
	return budget
}

func splitLines(s string) []string {
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	for line := range strings.Lines(s) {
		lines = append(lines, line)
	}
	return lines
}

// hardSplit cuts an oversized block into consecutive rune-safe pieces of at
// most chunkTokens estimated tokens. An empty piece is impossible: size is
// at least one byte, and a piece shorter than one rune is widened to it.
func hardSplit(block string, chunkTokens int) []string {
	size := max(chunkTokens*bytesPerToken, 1)
	var pieces []string
	for block != "" {
		piece := cutAt(block, size)
		if piece == "" {
			_, n := utf8.DecodeRuneInString(block)
			piece = block[:n]
		}
		pieces = append(pieces, piece)
		block = block[len(piece):]
	}
	return pieces
}
