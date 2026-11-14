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
// (reasoningKeep 0 in the older part).
type limits struct {
	textMax        int
	toolResultMax  int
	toolResultHead int
	toolResultTail int
	toolInputMax   int
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
		return header + "] " + capOpt(t.Text, l.textMax) + "\n"
	case session.AssistantText:
		return header + "] " + capOpt(t.Text, l.textMax) + "\n"
	case session.ToolCall:
		return renderToolCall(num, t, l)
	case session.ToolResult:
		if t.ToolID != "" {
			header += " id=" + t.ToolID
		}
		if t.IsError {
			header += " ERROR"
		}
		text := truncLines(t.Text, l.toolResultHead, l.toolResultTail, l.toolResultMax)
		return header + "] " + text + "\n"
	case session.Reasoning:
		if reasoningAfter >= l.reasoningKeep {
			return ""
		}
		return header + "] " + capOpt(t.Text, l.reasoningMax) + "\n"
	case session.CompactionSummary:
		return header + "] " + capOpt(t.Text, l.compactMax) + "\n"
	case session.Interrupted:
		return header + "]\n"
	default:
		return ""
	}
}

func renderToolCall(num int, t session.Turn, l limits) string {
	header := fmt.Sprintf("[#%d tool_call %s", num, t.ToolName)
	if t.ToolID != "" {
		header += " id=" + t.ToolID
	}
	return header + "] " + toolInput(t, l) + "\n"
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
		return editInput(t.Input, l.toolInputMax)
	}
	return capOpt(t.Input, l.toolInputMax)
}

const editInputKeep = 300

// editInput keeps the first editInputKeep chars of the input; when the file
// path is not already inside them it is prepended, so the target file
// survives truncation of a long JSON body.
func editInput(input string, max int) string {
	body := cutAt(input, editInputKeep)
	path := inputPath(input)
	if path == "" || strings.Contains(body, path) {
		return capHeadTail(body, max)
	}
	return capHeadTail("path: "+path+"\n"+body, max)
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
