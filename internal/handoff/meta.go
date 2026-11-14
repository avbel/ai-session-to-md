package handoff

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	metaFormat = 1
	// MetaPrefix and MetaSuffix wrap the meta JSON in an HTML comment that
	// is always the first line of a handoff file.
	MetaPrefix = "<!-- ai-session-to-md:meta "
	MetaSuffix = " -->"
	// dashEscape is a literal "-" followed by the JSON \u002d escape, so a
	// "--" inside a title or tldr cannot close the comment with "-->".
	dashEscape = `-\u002d`
)

// HistoryEntry is one row of the handoff chain carried in the meta comment.
type HistoryEntry struct {
	Harness     string `json:"harness"`
	SessionID   string `json:"session_id"`
	GeneratedAt string `json:"generated_at"`
	Host        string `json:"host,omitempty"`
	TLDR        string `json:"tldr,omitempty"`
}

// Meta is the JSON stored in the first line of every handoff file.
type Meta struct {
	Format      int            `json:"format"`
	ToolVersion string         `json:"tool_version"`
	Harness     string         `json:"harness"`
	SessionID   string         `json:"session_id"`
	Title       string         `json:"title"`
	GeneratedAt string         `json:"generated_at"`
	Host        string         `json:"host,omitempty"`
	Repo        string         `json:"repo,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	Head        string         `json:"head,omitempty"`
	Raw         bool           `json:"raw,omitzero"`
	TLDR        string         `json:"tldr,omitempty"`
	History     []HistoryEntry `json:"history,omitempty"`
}

// EncodeMeta marshals the meta and escapes every "--" so no data can close
// the surrounding HTML comment.
func EncodeMeta(m Meta) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encode handoff meta: %w", err)
	}
	return MetaPrefix + strings.ReplaceAll(string(b), "--", dashEscape) + MetaSuffix, nil
}

// ParseMeta parses the meta from the first line of a handoff file. BOM and
// carriage returns are stripped first. ok is false for anything that is not
// a complete, well-formed meta comment of format 1 with a session id.
func ParseMeta(line string) (Meta, bool) {
	line = strings.TrimPrefix(line, "\uFEFF")
	line = strings.ReplaceAll(line, "\r", "")
	if !strings.HasPrefix(line, MetaPrefix) || !strings.HasSuffix(line, MetaSuffix) {
		return Meta{}, false
	}
	body := line[len(MetaPrefix) : len(line)-len(MetaSuffix)]
	var m Meta
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return Meta{}, false
	}
	if m.Format < 1 || m.SessionID == "" {
		return Meta{}, false
	}
	return m, true
}

// FirstLine returns line 1 of a file's content, without its line ending.
func FirstLine(content string) string {
	if before, _, ok := strings.Cut(content, "\n"); ok {
		return strings.TrimSuffix(before, "\r")
	}
	return strings.TrimSuffix(content, "\r")
}

// EscapeCell makes s safe inside a Markdown table cell.
func EscapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}
