package session

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxMetaRunes bounds what counts as a meta request: a long message that
// mentions "summarize" is real work, not a request to save the session.
const maxMetaRunes = 100

// metaPhrases are requests to save or summarize the session itself, matched
// against the lowercased, punctuation-free message.
var metaPhrases = []string{
	"remember what we did",
	"remember what we have done",
	"remember what weve done",
	"remember this session",
	"remember the session",
	"summarize the session",
	"summarize this session",
	"summarise the session",
	"summarise this session",
	"summarize what we did",
	"summarise what we did",
	"save the session",
	"save this session",
	"save session",
	"save progress",
	"save the progress",
	"write a handoff",
	"write the handoff",
	"create a handoff",
	"make a handoff",
}

// metaCommands are slash commands that save or compact the session.
var metaCommands = []string{"/compact", "/save-session", "/handoff"}

// IsMetaRequest reports whether a user message asks to save, summarize or
// hand off the session rather than continue the task.
func IsMetaRequest(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxMetaRunes {
		return false
	}
	lower := strings.ToLower(text)
	for _, cmd := range metaCommands {
		if lower == cmd || strings.HasPrefix(lower, cmd+" ") {
			return true
		}
	}
	norm := normalizeMeta(lower)
	return slices.ContainsFunc(metaPhrases, func(p string) bool {
		return strings.Contains(norm, p)
	})
}

// normalizeMeta drops punctuation and collapses whitespace so "Remember
// what we've done!" matches "remember what weve done".
func normalizeMeta(s string) string {
	kept := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return r
		}
		return -1
	}, s)
	return strings.Join(strings.Fields(kept), " ")
}

// withoutTrailingMeta cuts the trailing meta requests (and the replies to
// them) off turns, so the last user message is the last real instruction.
// A session whose only user messages are meta requests is returned whole.
func withoutTrailingMeta(turns []Turn) []Turn {
	cut := turns
	for {
		i := lastIndexOf(cut, UserText)
		if i < 0 || !IsMetaRequest(cut[i].Text) {
			return cut
		}
		if lastIndexOf(cut[:i], UserText) < 0 {
			return cut
		}
		cut = cut[:i]
	}
}

func lastIndexOf(turns []Turn, kind TurnKind) int {
	for i, turn := range slices.Backward(turns) {
		if turn.Kind == kind {
			return i
		}
	}
	return -1
}
