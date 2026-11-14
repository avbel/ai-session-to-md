package reduce

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// limits holds the per-kind truncation caps for one rendering pass, in
// bytes. A zero cap means uncapped (tail compaction summaries) or dropped
// (reasoningKeep 0 in the older part). editKeep is the fixed head cut for
// edit/write tool inputs in the older part; 0 (the tail window) means the
// input is capped like any other input instead.
type limits struct {
	textMax        int
	toolResultMax  int
	toolResultHead int
	toolResultTail int
	toolInputMax   int
	editKeep       int
	reasoningKeep  int
	reasoningMax   int
	compactMax     int
}

func tailLimits() limits {
	return limits{
		textMax:        20000,
		toolResultMax:  6000,
		toolResultHead: 40,
		toolResultTail: 40,
		toolInputMax:   3000,
		reasoningKeep:  3,
		reasoningMax:   2000,
	}
}

func olderLimits() limits {
	return limits{
		textMax:        6000,
		toolResultMax:  1200,
		toolResultHead: 10,
		toolResultTail: 10,
		toolInputMax:   600,
		editKeep:       300,
		compactMax:     8000,
	}
}

// halve halves every limit, for the tail-window shrinking of §11 step 3.
func (l limits) halve() limits {
	return limits{
		textMax:        l.textMax / 2,
		toolResultMax:  l.toolResultMax / 2,
		toolResultHead: l.toolResultHead / 2,
		toolResultTail: l.toolResultTail / 2,
		toolInputMax:   l.toolInputMax / 2,
		editKeep:       l.editKeep / 2,
		reasoningKeep:  l.reasoningKeep / 2,
		reasoningMax:   l.reasoningMax / 2,
		compactMax:     l.compactMax / 2,
	}
}

// timeLayout matches the plan's example block: minute precision, UTC.
const timeLayout = "2006-01-02T15:04Z"

// renderTurn renders one turn as a single block. num is the 1-based position
// of the turn in the full session, so dropped turns leave numbering gaps.
// reasoningAfter counts the reasoning turns after this one: a reasoning turn
// is kept only when fewer than reasoningKeep follow it.
func renderTurn(num int, t session.Turn, l limits, reasoningAfter int) string {
	header := fmt.Sprintf("[#%d %s", num, t.Kind)
	switch t.Kind {
	case session.UserText:
		if !t.Time.IsZero() {
			header += " " + t.Time.UTC().Format(timeLayout)
		}
		return header + "] " + escapeBlocks(capOpt(t.Text, l.textMax)) + "\n"
	case session.AssistantText:
		return header + "] " + escapeBlocks(capOpt(t.Text, l.textMax)) + "\n"
	case session.ToolCall:
		return renderToolCall(num, t, l)
	case session.ToolResult:
		if t.ToolID != "" {
			header += " id=" + t.ToolID
		}
		if t.IsError {
			header += " ERROR"
		}
		text := escapeBlocks(truncLines(t.Text, l.toolResultHead, l.toolResultTail, l.toolResultMax))
		return header + "] " + text + "\n"
	case session.Reasoning:
		if reasoningAfter >= l.reasoningKeep {
			return ""
		}
		return header + "] " + escapeBlocks(capOpt(t.Text, l.reasoningMax)) + "\n"
	case session.CompactionSummary:
		return header + "] " + escapeBlocks(capOpt(t.Text, l.compactMax)) + "\n"
	case session.Interrupted:
		return header + "]\n"
	default:
		return ""
	}
}

// blockEscape is prefixed to any body line starting with "[#", which a
// transcript body could otherwise use to forge a block header. The
// backslash keeps the text readable for the LLM while making it impossible
// to parse as a "[#N kind …]" block start.
const blockEscape = `\`

// escapeBlocks escapes body lines that start with "[#", so turn text cannot
// forge transcript blocks. It runs after truncation, since a cut can leave
// a line starting with "[#".
func escapeBlocks(s string) string {
	if !strings.Contains(s, "[#") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for line := range strings.Lines(s) {
		if strings.HasPrefix(line, "[#") {
			b.WriteString(blockEscape)
		}
		b.WriteString(line)
	}
	return b.String()
}

func renderToolCall(num int, t session.Turn, l limits) string {
	header := fmt.Sprintf("[#%d tool_call %s", num, t.ToolName)
	if t.ToolID != "" {
		header += " id=" + t.ToolID
	}
	return header + "] " + escapeBlocks(toolInput(t, l)) + "\n"
}

// editWriteTools are the tools whose input targets a file. For these the
// older part keeps the path plus the first 300 chars (§11 step 2).
var editWriteTools = map[string]struct{}{
	"Edit":         {},
	"Write":        {},
	"MultiEdit":    {},
	"NotebookEdit": {},
	"edit":         {},
	"write":        {},
	"multiedit":    {},
	"apply_patch":  {},
	"patch":        {},
}

func toolInput(t session.Turn, l limits) string {
	if _, ok := editWriteTools[t.ToolName]; ok {
		return editInput(t.Input, l)
	}
	return capOpt(t.Input, l.toolInputMax)
}

// editInput shapes an edit/write tool input. With editKeep > 0 (the older
// part) it keeps the first editKeep chars; with editKeep == 0 (the tail
// window) it keeps the whole input, capped head+tail, so the end of a long
// edit survives. The file path is prepended when it is not already inside
// the kept text, so the target file always survives truncation.
func editInput(input string, l limits) string {
	body := input
	if l.editKeep > 0 {
		body = cutAt(input, l.editKeep)
	}
	path := inputPath(input)
	if path != "" && !strings.Contains(body, path) {
		body = "path: " + path + "\n" + body
	}
	return capOpt(body, l.toolInputMax)
}

var (
	jsonPathRe  = regexp.MustCompile(`"(?:file_path|notebook_path|filePath|path)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	patchPathRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add File|Update File|Delete File|Move to): (\S+)`)
)

// inputPath extracts the target file path from a tool input: the JSON value
// of a known path key, else a patch header path.
func inputPath(input string) string {
	if m := jsonPathRe.FindStringSubmatch(input); m != nil {
		var p string
		if json.Unmarshal([]byte(`"`+m[1]+`"`), &p) == nil {
			return p
		}
		return m[1]
	}
	if m := patchPathRe.FindStringSubmatch(input); m != nil {
		return m[1]
	}
	return ""
}

// renderAll renders every turn of the session, numbering each block with its
// 1-based position. Reasoning turns outside the last reasoningKeep are
// dropped; dropped turns leave gaps in the numbering.
func renderAll(turns []session.Turn, l limits) []string {
	blocks := make([]string, len(turns))
	reasoningAfter := 0
	for i := len(turns) - 1; i >= 0; i-- {
		blocks[i] = renderTurn(i+1, turns[i], l, reasoningAfter)
		if turns[i].Kind == session.Reasoning {
			reasoningAfter++
		}
	}
	return blocks
}
