package claudecode

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

var (
	systemReminderRe  = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	commandNameRe     = regexp.MustCompile(`<command-name>(.*?)</command-name>`)
	commandArgsRe     = regexp.MustCompile(`(?s)<command-args[^>]*>(.*?)</command-args>`)
	commandArgsAttrRe = regexp.MustCompile(`<command-args[^>]*\bcontent="([^"]*)"`)
)

// userTurns converts one user entry into turns: a compaction summary, text
// turns, tool results, or image placeholders.
func userTurns(ln line) []session.Turn {
	t := turnTime(ln)
	if ln.IsMeta {
		return nil
	}
	if ln.IsCompactSummary {
		if txt := strings.TrimSpace(contentString(ln.Message.Content)); txt != "" {
			return []session.Turn{{Kind: session.CompactionSummary, Time: t, Text: txt}}
		}
		return nil
	}
	var s string
	if json.Unmarshal(ln.Message.Content, &s) == nil {
		return textTurns(s, t)
	}
	var turns []session.Turn
	for _, b := range contentBlocks(ln.Message.Content) {
		switch b.Type {
		case "text":
			turns = append(turns, textTurns(b.Text, t)...)
		case "image":
			turns = append(turns, session.Turn{Kind: session.UserText, Time: t, Text: "[image]"})
		case "tool_result":
			turns = append(turns, session.Turn{
				Kind:    session.ToolResult,
				Time:    t,
				ToolID:  b.ToolUseID,
				IsError: b.IsError,
				Text:    resultText(b.Content),
			})
		}
	}
	return turns
}

// textTurns applies the user text rules: strip system reminders, skip
// caveats and stdout, recognize command and interrupt markers, drop empty
// text.
func textTurns(text string, t time.Time) []session.Turn {
	text = systemReminderRe.ReplaceAllString(text, "")
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "<local-command-caveat>") ||
		strings.HasPrefix(trimmed, "<local-command-stdout>") {
		return nil
	}
	if name, args, ok := commandMessage(trimmed); ok {
		return []session.Turn{{Kind: session.UserText, Time: t, Text: strings.TrimSpace(name + " " + args)}}
	}
	if strings.HasPrefix(trimmed, "[Request interrupted by user") {
		return []session.Turn{{Kind: session.Interrupted, Time: t}}
	}
	return []session.Turn{{Kind: session.UserText, Time: t, Text: trimmed}}
}

// commandMessage reports a slash-command user message and its arguments,
// taken from the <command-args> element text or a content attribute.
func commandMessage(text string) (string, string, bool) {
	m := commandNameRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	name := m[1]
	args := ""
	if am := commandArgsRe.FindStringSubmatch(text); am != nil && strings.TrimSpace(am[1]) != "" {
		args = strings.TrimSpace(am[1])
	} else if am := commandArgsAttrRe.FindStringSubmatch(text); am != nil {
		args = am[1]
	}
	return name, args, true
}

// assistantTurns converts assistant content blocks into turns: text,
// thinking, and tool calls; other block types are ignored.
func assistantTurns(ln line) []session.Turn {
	t := turnTime(ln)
	var s string
	if json.Unmarshal(ln.Message.Content, &s) == nil {
		if s = strings.TrimSpace(s); s != "" {
			return []session.Turn{{Kind: session.AssistantText, Time: t, Text: s}}
		}
		return nil
	}
	var turns []session.Turn
	for _, b := range contentBlocks(ln.Message.Content) {
		switch b.Type {
		case "text":
			if b.Text = strings.TrimSpace(b.Text); b.Text != "" {
				turns = append(turns, session.Turn{Kind: session.AssistantText, Time: t, Text: b.Text})
			}
		case "thinking":
			if b.Thinking != "" {
				turns = append(turns, session.Turn{Kind: session.Reasoning, Time: t, Text: b.Thinking})
			}
		case "tool_use", "server_tool_use":
			turns = append(turns, session.Turn{
				Kind:     session.ToolCall,
				Time:     t,
				ToolID:   b.ID,
				ToolName: b.Name,
				Input:    compactJSON(b.Input),
			})
		}
	}
	return turns
}

// resultText flattens a tool_result content: a string as is, an array of
// text/image items joined with newlines, anything else compacted to JSON.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []block
	if json.Unmarshal(raw, &items) == nil {
		var parts []string
		for _, it := range items {
			switch it.Type {
			case "text":
				parts = append(parts, it.Text)
			case "image":
				parts = append(parts, "[image]")
			}
		}
		return strings.Join(parts, "\n")
	}
	return compactJSON(raw)
}
