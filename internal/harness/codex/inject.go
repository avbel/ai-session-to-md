package codex

import (
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// injectedPrefixes are user-message texts that carry harness-injected
// context rather than something the user said. A leading "<" followed by
// any of these tag names (prefix match) means injected too.
var injectedPrefixes = []string{
	"# AGENTS.md instructions",
	"You are in a git worktree",
}

var injectedTags = []string{
	"environment_context",
	"user_instructions",
	"INSTRUCTIONS",
	"skill",
	"permissions",
	"collaboration_mode",
	"cwd",
	"external_codex_apps",
	"current_",
	"send_user_message_question",
}

// turnAbortedTag marks the turn the user aborted, rendered as Interrupted.
const turnAbortedTag = "turn_aborted"

// classifyUserText decides what a user input_text content item becomes:
// nothing (injected context), an Interrupted turn, or a UserText turn.
// Unknown tag-led texts are logged so new injections can be added.
func classifyUserText(text string, log func(string, ...any)) (session.Turn, bool) {
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(text, p) {
			return session.Turn{}, false
		}
	}
	if after, ok := strings.CutPrefix(text, "<"); ok {
		tag := after
		if strings.HasPrefix(tag, turnAbortedTag) {
			return session.Turn{Kind: session.Interrupted, Text: text}, true
		}
		for _, t := range injectedTags {
			if strings.HasPrefix(tag, t) {
				return session.Turn{}, false
			}
		}
		log("unknown injected tag in codex user text: %s", firstTagWord(tag))
	}
	if strings.TrimSpace(text) == "" {
		return session.Turn{}, false
	}
	return session.Turn{Kind: session.UserText, Text: text}, true
}

// firstTagWord names the tag for the verbose log.
func firstTagWord(tag string) string {
	if i := strings.IndexAny(tag, ">\n\t "); i >= 0 {
		return "<" + tag[:i]
	}
	return "<" + tag
}
