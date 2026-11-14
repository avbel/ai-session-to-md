package opencode

import (
	"cmp"
	"encoding/json"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

type msgData struct {
	Text    string          `json:"text"`
	Files   json.RawMessage `json:"files"`
	Model   json.RawMessage `json:"model"`
	Content []contentItem   `json:"content"`
	Finish  *string         `json:"finish"`
	Summary string          `json:"summary"`
}

type contentItem struct {
	Type  string    `json:"type"`
	Text  string    `json:"text"`
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	State toolState `json:"state"`
}

type toolState struct {
	Status  string          `json:"status"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
	Error   string          `json:"error"`
}

// msg is one transcript entry, from either the session_message rows (Type and
// Time from the row, Data from the data column) or an export document (Type
// and Data from the same JSON object).
type msg struct {
	Type string
	Data msgData
	Time time.Time
}

func parseMsgData(data []byte) (msgData, error) {
	var md msgData
	if err := json.Unmarshal(data, &md); err != nil {
		return msgData{}, err
	}
	return md, nil
}

// builder folds messages, in seq order, into a normalized session. It is
// shared by the v2 database path and the export fallback.
type builder struct {
	base     session.Meta
	model    string
	ver      string
	turns    []session.Turn
	skipped  int
	todoJSON string
	files    []string
	seen     map[string]struct{}
	info     session.Info
}

func newBuilder(base session.Meta) *builder {
	return &builder{base: base, seen: map[string]struct{}{}}
}

func (b *builder) addMsg(m msg) {
	switch m.Type {
	case "user":
		b.addUser(m)
	case "assistant":
		b.addAssistant(m)
	case "compaction":
		if m.Data.Summary != "" {
			b.append(session.Turn{Kind: session.CompactionSummary, Time: m.Time, Text: m.Data.Summary})
		}
	case "synthetic", "system", "idle", "agent-switched", "model-switched":
		// Injected reminders and state flips carry no transcript value, and
		// synthetic text may contain secrets: never surface it.
	default:
		b.skipped++
	}
}

func (b *builder) addUser(m msg) {
	text := m.Data.Text
	for _, name := range fileNames(m.Data.Files) {
		if text == "" {
			text = "[file: " + name + "]"
		} else {
			text += " [file: " + name + "]"
		}
	}
	if strings.TrimSpace(text) == "" {
		b.skipped++
		return
	}
	b.append(session.Turn{Kind: session.UserText, Time: m.Time, Text: text})
}

func (b *builder) addAssistant(m msg) {
	b.model = cmp.Or(modelJSON(string(m.Data.Model)), b.model)
	// The finish of the last assistant message is the one that counts; later
	// messages simply overwrite.
	b.info.HasFinish = true
	b.info.Finish = cmp.Or(deref(m.Data.Finish), "")
	for _, item := range m.Data.Content {
		b.addContent(m.Time, item)
	}
}

func (b *builder) addContent(t time.Time, item contentItem) {
	switch item.Type {
	case "text":
		if strings.TrimSpace(item.Text) != "" {
			b.append(session.Turn{Kind: session.AssistantText, Time: t, Text: item.Text})
		}
	case "reasoning":
		if strings.TrimSpace(item.Text) != "" {
			b.append(session.Turn{Kind: session.Reasoning, Time: t, Text: item.Text})
		}
	case "tool":
		b.addTool(t, item)
	default:
		b.skipped++
	}
}

func (b *builder) addTool(t time.Time, item contentItem) {
	if strings.EqualFold(item.Name, "todowrite") {
		b.todoJSON = compactJSON(item.State.Input)
	}
	b.append(session.Turn{
		Kind:     session.ToolCall,
		Time:     t,
		ToolID:   item.ID,
		ToolName: item.Name,
		Input:    compactJSON(item.State.Input),
	})
	switch item.State.Status {
	case "completed":
		b.append(session.Turn{Kind: session.ToolResult, Time: t, ToolID: item.ID, Text: resultText(item.State.Content)})
	case "error":
		b.append(session.Turn{Kind: session.ToolResult, Time: t, ToolID: item.ID, IsError: true, Text: item.State.Error})
	default:
		// pending/running: no result turn; session.Derive flags the dangling
		// call as "stopped during tool call".
	}
	b.noteFiles(item)
}

func (b *builder) append(t session.Turn) {
	b.turns = append(b.turns, t)
}

// build finishes the session; todos come from the todo table when available
// and otherwise from the last todowrite tool input.
func (b *builder) build(todos []session.Todo) session.Session {
	if len(todos) == 0 {
		todos = b.todoFallback()
	}
	return session.Session{
		Meta:         b.base,
		Model:        b.model,
		HarnessVer:   b.ver,
		Turns:        b.turns,
		Todos:        todos,
		FilesTouched: b.files,
		Skipped:      b.skipped,
	}
}

func (b *builder) todoFallback() []session.Todo {
	if b.todoJSON == "" {
		return nil
	}
	var in struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if json.Unmarshal([]byte(b.todoJSON), &in) != nil {
		return nil
	}
	var out []session.Todo
	for _, t := range in.Todos {
		if strings.TrimSpace(t.Content) == "" {
			continue
		}
		out = append(out, session.Todo{Content: t.Content, Status: normTodoStatus(t.Status)})
	}
	return out
}

func normTodoStatus(s string) string {
	if s = strings.ToLower(strings.TrimSpace(s)); s == "" {
		return "pending"
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
