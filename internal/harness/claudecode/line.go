package claudecode

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"time"
)

// line is the subset of Claude Code JSONL fields this adapter needs; all
// other fields are ignored during unmarshal.
type line struct {
	Type              string  `json:"type"`
	Subtype           string  `json:"subtype"`
	UUID              string  `json:"uuid"`
	ParentUUID        string  `json:"parentUuid"`
	LogicalParentUUID string  `json:"logicalParentUuid"`
	IsSidechain       bool    `json:"isSidechain"`
	IsMeta            bool    `json:"isMeta"`
	IsCompactSummary  bool    `json:"isCompactSummary"`
	Timestamp         string  `json:"timestamp"`
	Cwd               string  `json:"cwd"`
	GitBranch         string  `json:"gitBranch"`
	Version           string  `json:"version"`
	Message           message `json:"message"`
	AITitle           string  `json:"aiTitle"`
	CustomTitle       string  `json:"customTitle"`
	LastPrompt        string  `json:"lastPrompt"`
}

type message struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

// block is one entry of a message content array, reused for both assistant
// blocks and user tool_result/image item shapes.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

const (
	typeUser      = "user"
	typeAssistant = "assistant"
	typeSystem    = "system"
)

func turnTime(ln line) time.Time {
	if ln.Timestamp == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339, ln.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// compactJSON renders a raw JSON value in compact form, falling back to the
// raw bytes when they are not valid JSON.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(bytes.TrimSpace(raw))
	}
	return buf.String()
}

// contentBlocks decodes a message content array, or wraps a single block
// object, tolerating shapes the transcript occasionally produces.
func contentBlocks(raw json.RawMessage) []block {
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}
	var b block
	if err := json.Unmarshal(raw, &b); err == nil && b.Type != "" {
		return []block{b}
	}
	return nil
}

func contentString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func slogWarn(msg, dir string, err error) {
	slog.Warn(msg, "dir", dir, "err", err)
}
