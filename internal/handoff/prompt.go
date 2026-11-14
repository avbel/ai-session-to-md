// Package handoff assembles the Markdown handoff file: the embedded prompts,
// output validation, the meta HTML comment, previous-handoff selection and
// rendering. Every string it receives must already be redacted; it does not
// import redact, llm, gitinfo or reduce — the wiring happens one layer up.
package handoff

import (
	"embed"
	"strings"
)

//go:embed prompts/*.md
var promptFS embed.FS

func mustPrompt(name string) string {
	b, err := promptFS.ReadFile("prompts/" + name)
	if err != nil {
		panic("handoff: embedded prompt " + name + ": " + err.Error())
	}
	return string(b)
}

var (
	systemPromptText = mustPrompt("system.md")
	chunkPromptText  = mustPrompt("chunk.md")
	rerollPromptText = mustPrompt("reroll.md")
)

// SystemPrompt returns the built-in system prompt with additional user
// instructions appended, each as its own "Additional instructions" block.
// When both --prompt-file and --prompt-append are set, the file text comes
// first in extra.
func SystemPrompt(extra []string) string {
	var b strings.Builder
	b.WriteString(systemPromptText)
	for _, e := range extra {
		if strings.TrimSpace(e) == "" {
			continue
		}
		b.WriteString("\n\nAdditional instructions from the user:\n")
		b.WriteString(e)
	}
	return b.String()
}

// ChunkPrompt is the map-step prompt whose output is fed back as
// [earlier history notes].
func ChunkPrompt() string {
	return chunkPromptText
}

// EscapeData stops literal content from closing its XML-ish wrapper: data
// containing "</transcript>" would otherwise end the <transcript> tag early.
// Every "</" becomes "<\/", which the reader treats as plain text.
func EscapeData(s string) string {
	return strings.ReplaceAll(s, "</", `<\/`)
}

// UserMessageInput is the LLM user message for prompts/system.md. Each part
// is escaped so its content cannot close its own wrapper tag.
type UserMessageInput struct {
	PreviousHandoff string
	SessionFacts    string
	Transcript      string
}

func UserMessage(in UserMessageInput) string {
	var b strings.Builder
	b.WriteString("<previous_handoff>\n")
	b.WriteString(EscapeData(in.PreviousHandoff))
	b.WriteString("\n</previous_handoff>\n<session_facts>\n")
	b.WriteString(EscapeData(in.SessionFacts))
	b.WriteString("\n</session_facts>\n<transcript>\n")
	b.WriteString(EscapeData(in.Transcript))
	b.WriteString("\n</transcript>")
	return b.String()
}

// RerollMessage formats prompts/reroll.md with the missing required parts,
// e.g. ["TL;DR:", "### Tried and failed"] joined with ", ".
func RerollMessage(missing []string) string {
	return strings.ReplaceAll(rerollPromptText, "{missing}", strings.Join(missing, ", "))
}

// TruncateChunkNotes caps chunk notes at maxWords words, cutting on a bullet
// boundary: the first line that would overflow is dropped whole. When no
// complete line fits, the first maxWords words are kept.
func TruncateChunkNotes(notes string, maxWords int) string {
	if maxWords <= 0 || len(strings.Fields(notes)) <= maxWords {
		return notes
	}
	var kept []string
	total := 0
	for line := range strings.Lines(notes) {
		w := len(strings.Fields(line))
		if total+w > maxWords {
			break
		}
		total += w
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return strings.Join(strings.Fields(notes)[:maxWords], " ")
	}
	return strings.TrimRight(strings.Join(kept, ""), "\n")
}
