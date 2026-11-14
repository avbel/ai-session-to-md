package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// applyEventMsg collects the end-of-session signals (§7.2) and FileChange
// paths; other event subtypes are ignored.
func (s *loadState) applyEventMsg(raw json.RawMessage, log func(string, ...any)) {
	var e eventMsgPayload
	if err := json.Unmarshal(raw, &e); err != nil {
		s.skipped++
		return
	}

	switch e.Type {
	case "task_started":
		s.midTurn = true
	case "task_complete":
		s.midTurn = false
	case "turn_aborted":
		s.midTurn = false
		s.append(session.Turn{Kind: session.Interrupted, Time: s.now})
	case "item_completed":
		s.applyItemCompleted(e.Item)
	}
}

// applyItemCompleted records FilesTouched from FileChange items: the change
// map's keys, or each array element's path.
func (s *loadState) applyItemCompleted(item json.RawMessage) {
	var it struct {
		Type    string          `json:"type"`
		Changes json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(item, &it); err != nil {
		return
	}
	if it.Type != "FileChange" {
		return
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(it.Changes, &obj); err == nil {
		keys := make([]string, 0, len(obj))
		for path := range obj {
			keys = append(keys, path)
		}
		slices.Sort(keys)
		for _, path := range keys {
			s.addFile(path)
		}
		return
	}
	var elems []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(it.Changes, &elems); err == nil {
		for _, el := range elems {
			if el.Path != "" {
				s.addFile(el.Path)
			}
		}
	}
}

func (s *loadState) addFile(path string) {
	if path == "" || slicesContains(s.files, path) {
		return
	}
	s.files = append(s.files, path)
}

func slicesContains(list []string, v string) bool {
	return slices.Contains(list, v)
}

// applyTodos replaces the todos when an update_plan tool call is seen: the
// last one in the stream wins.
func (s *loadState) applyTodos(call callPayload) {
	var args struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return
	}
	if len(args.Plan) == 0 {
		return
	}
	s.todos = s.todos[:0]
	for _, p := range args.Plan {
		s.todos = append(s.todos, session.Todo{
			Content: p.Step,
			Status:  normalizeTodoStatus(p.Status),
		})
	}
}

// normalizeTodoStatus maps plan statuses onto the session.Todo vocabulary.
func normalizeTodoStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return "pending"
	}
	return s
}
