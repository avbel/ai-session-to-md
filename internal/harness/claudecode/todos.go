package claudecode

import (
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// taskIDRe extracts the numeric task id from a TaskCreate tool_result text
// or a TaskUpdate taskId, with or without a leading '#'.
var taskIDRe = regexp.MustCompile(`#?(\d+)`)

// todoScanner replays the todo state in file order: the last TodoWrite
// resets the list, and newer TaskCreate/TaskUpdate calls are applied on
// top. TaskCreate entries get their numeric id later, from the matching
// tool_result text.
type todoScanner struct {
	todos   []session.Todo
	ids     map[string]int
	pending []pendingCreate
}

type pendingCreate struct {
	toolUseID string
	todoIndex int
}

// toolUses consumes the tool_use blocks of one assistant entry.
func (ts *todoScanner) toolUses(blocks []block) {
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		switch b.Name {
		case "TodoWrite":
			if todos, ok := todoWriteInput(b.Input); ok {
				ts.reset(todos)
			}
		case "TaskCreate":
			if subject := taskCreateSubject(b.Input); subject != "" {
				ts.todos = append(ts.todos, session.Todo{Content: subject, Status: "pending"})
				ts.pending = append(ts.pending, pendingCreate{toolUseID: b.ID, todoIndex: len(ts.todos) - 1})
			}
		case "TaskUpdate":
			ts.taskUpdate(b.Input)
		}
	}
}

// toolResults consumes the tool_result blocks of one user entry, binding
// awaited TaskCreate calls to their numeric ids.
func (ts *todoScanner) toolResults(blocks []block) {
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		for i, p := range ts.pending {
			if p.toolUseID != b.ToolUseID {
				continue
			}
			if m := taskIDRe.FindStringSubmatch(resultText(b.Content)); m != nil {
				if ts.ids == nil {
					ts.ids = make(map[string]int)
				}
				ts.ids[m[1]] = p.todoIndex
			}
			ts.pending = slices.Delete(ts.pending, i, i+1)
			break
		}
	}
}

func (ts *todoScanner) reset(todos []session.Todo) {
	ts.todos = todos
	ts.ids = nil
	ts.pending = nil
}

func (ts *todoScanner) taskUpdate(raw json.RawMessage) {
	var in struct {
		TaskID string `json:"taskId"`
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Status == "" {
		return
	}
	m := taskIDRe.FindStringSubmatch(in.TaskID)
	if m == nil {
		return
	}
	idx, ok := ts.ids[m[1]]
	if !ok || idx >= len(ts.todos) {
		return
	}
	ts.todos[idx].Status = normStatus(in.Status)
}

type todoWritePayload struct {
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}

func todoWriteInput(raw json.RawMessage) ([]session.Todo, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var in todoWritePayload
	if json.Unmarshal(raw, &in) != nil {
		return nil, false
	}
	out := make([]session.Todo, 0, len(in.Todos))
	for _, t := range in.Todos {
		if strings.TrimSpace(t.Content) == "" {
			continue
		}
		out = append(out, session.Todo{Content: t.Content, Status: normStatus(t.Status)})
	}
	return out, true
}

func taskCreateSubject(raw json.RawMessage) string {
	var in struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	return cmp.Or(strings.TrimSpace(in.Subject), strings.TrimSpace(in.Description))
}

func normStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, " ", "_")
}

var fileTools = map[string]struct{}{
	"Edit":         {},
	"Write":        {},
	"MultiEdit":    {},
	"NotebookEdit": {},
}

type pathInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
}

func inputPath(raw json.RawMessage) string {
	var in pathInput
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	return cmp.Or(in.FilePath, in.NotebookPath)
}

// appendFiles collects file paths from Edit/Write/MultiEdit/NotebookEdit
// tool inputs, deduplicated in first-seen order.
func appendFiles(dst []string, blocks []block, seen map[string]bool) []string {
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		if _, ok := fileTools[b.Name]; !ok {
			continue
		}
		p := inputPath(b.Input)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		dst = append(dst, p)
	}
	return dst
}
