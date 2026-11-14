package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

type messagePayload struct {
	Role    string        `json:"role"`
	Content []contentItem `json:"content"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type agentMessagePayload struct {
	Recipient string          `json:"recipient"`
	Content   json.RawMessage `json:"content"`
}

type reasoningPayload struct {
	Summary []contentItem `json:"summary"`
}

type callPayload struct {
	Name      string  `json:"name"`
	CallID    *string `json:"call_id"`
	Arguments string  `json:"arguments"`
	Input     string  `json:"input"`
}

type outputPayload struct {
	CallID *string         `json:"call_id"`
	Output json.RawMessage `json:"output"`
}

// applyResponseItem dispatches on payload.type; unknown types are ignored.
func (s *loadState) applyResponseItem(raw json.RawMessage, log func(string, ...any)) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		s.skipped++
		return
	}

	switch head.Type {
	case "message":
		s.applyMessage(raw, log)
	case "agent_message":
		s.applyAgentMessage(raw)
	case "reasoning":
		s.applyReasoning(raw)
	case "function_call":
		s.applyCall(raw, false)
	case "custom_tool_call":
		s.applyCall(raw, true)
	case "function_call_output", "custom_tool_call_output":
		s.applyOutput(raw)
	}
}

func (s *loadState) applyMessage(raw json.RawMessage, log func(string, ...any)) {
	var m messagePayload
	if err := json.Unmarshal(raw, &m); err != nil {
		s.skipped++
		return
	}
	switch m.Role {
	case "user":
		for _, item := range m.Content {
			s.applyUserItem(item, log)
		}
	case "assistant":
		for _, item := range m.Content {
			if item.Type == "output_text" && item.Text != "" {
				s.append(session.Turn{Kind: session.AssistantText, Time: s.now, Text: item.Text})
			}
		}
	}
}

func (s *loadState) applyUserItem(item contentItem, log func(string, ...any)) {
	switch item.Type {
	case "input_text":
		if turn, ok := classifyUserText(item.Text, log); ok {
			turn.Time = s.now
			s.append(turn)
		}
	case "input_image":
		s.append(session.Turn{Kind: session.UserText, Time: s.now, Text: "[image]"})
	}
}

func (s *loadState) applyAgentMessage(raw json.RawMessage) {
	var am agentMessagePayload
	if err := json.Unmarshal(raw, &am); err != nil {
		s.skipped++
		return
	}
	text := agentMessageText(am.Content)
	if text == "" {
		return
	}
	if am.Recipient != "" {
		text = "[to " + am.Recipient + "] " + text
	}
	s.append(session.Turn{Kind: session.AssistantText, Time: s.now, Text: text})
}

// agentMessageText accepts a plain string or an array of {text} items.
func agentMessageText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var items []contentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		texts := make([]string, 0, len(items))
		for _, it := range items {
			if it.Text != "" {
				texts = append(texts, it.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func (s *loadState) applyReasoning(raw json.RawMessage) {
	var r reasoningPayload
	if err := json.Unmarshal(raw, &r); err != nil {
		s.skipped++
		return
	}
	texts := make([]string, 0, len(r.Summary))
	for _, it := range r.Summary {
		if it.Text != "" {
			texts = append(texts, it.Text)
		}
	}
	if len(texts) == 0 {
		return
	}
	s.append(session.Turn{Kind: session.Reasoning, Time: s.now, Text: strings.Join(texts, "\n")})
}

// applyCall records function_call (arguments) and custom_tool_call (input).
func (s *loadState) applyCall(raw json.RawMessage, custom bool) {
	var c callPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		s.skipped++
		return
	}
	in := c.Arguments
	if custom {
		in = c.Input
	}
	s.addCall(session.Turn{
		Kind:     session.ToolCall,
		Time:     s.now,
		ToolName: c.Name,
		ToolID:   derefCallID(c.CallID),
		Input:    in,
	})
	if c.Name == "update_plan" {
		s.applyTodos(c)
	}
}

// applyOutput records tool results; a null call_id pairs with the most
// recent unpaired call.
func (s *loadState) applyOutput(raw json.RawMessage) {
	var o outputPayload
	if err := json.Unmarshal(raw, &o); err != nil {
		s.skipped++
		return
	}
	s.addResult(derefCallID(o.CallID), outputText(o.Output))
}

func derefCallID(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// addCall appends a ToolCall, remembers it as unpaired, and collects file
// paths from patch inputs. A call without call_id gets a synthetic id so a
// later null-id result can still pair with it unambiguously.
func (s *loadState) addCall(t session.Turn) {
	if t.ToolID == "" {
		t.ToolID = fmt.Sprintf("codex-call-%d", len(s.turns))
	}
	s.turns = append(s.turns, t)
	s.openCalls = append(s.openCalls, len(s.turns)-1)
	for _, path := range patchPaths(t.ToolName, t.Input) {
		s.addFile(path)
	}
}

// addResult appends a ToolResult. An empty id takes the ToolID of the most
// recent still-unpaired call; a known id clears that call from the list.
func (s *loadState) addResult(id, text string) {
	if id == "" {
		if n := len(s.openCalls); n > 0 {
			id = s.turns[s.openCalls[n-1]].ToolID
			s.openCalls = s.openCalls[:n-1]
		}
	} else {
		s.openCalls = slices.DeleteFunc(s.openCalls, func(i int) bool {
			return s.turns[i].ToolID == id
		})
	}
	s.append(session.Turn{Kind: session.ToolResult, Time: s.now, ToolID: id, Text: text})
}

// outputText normalizes the three known output shapes: string, array of
// {type,text}, or object with a content/output string (else compact JSON).
func outputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var items []contentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		texts := make([]string, 0, len(items))
		for _, it := range items {
			if it.Text != "" {
				texts = append(texts, it.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, key := range []string{"content", "output"} {
			var s string
			if err := json.Unmarshal(obj[key], &s); err == nil {
				return s
			}
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	return string(raw)
}
