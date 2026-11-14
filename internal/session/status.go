package session

import (
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxPendingInput = 1500
	questionSuffix  = "?"
)

// questionTools are tool names whose purpose is to ask the user something.
var questionTools = map[string]struct{}{
	"AskUserQuestion":          {},
	"ask_user_question":        {},
	"request_user_input":       {},
	"request_user_input_async": {},
	"question":                 {},
}

// Info carries harness-specific end-of-session signals that have no Turn
// representation: Codex event markers and the OpenCode finish state.
// HasFinish distinguishes "finish was recorded as absent" from "no finish
// information at all" (Claude Code, Codex), which must not read as incomplete.
type Info struct {
	MidTurn   bool
	Finish    string
	HasFinish bool
}

type Status struct {
	Text             string
	PendingToolName  string
	PendingToolInput string
	LastUser         string
	LastAssistant    string
	MidTurn          bool
	Finish           string
}

// Derive reports where the session stopped, working through the rules of the
// plan in order: interrupted, pending tool call, mid-turn / finish states,
// unanswered user message, open question, completed.
func Derive(s Session, info Info) Status {
	st := Status{
		LastUser:      lastTurnText(s.Turns, UserText),
		LastAssistant: lastTurnText(s.Turns, AssistantText),
		MidTurn:       info.MidTurn,
		Finish:        info.Finish,
	}

	if lastIs(s.Turns, Interrupted) {
		st.Text = "interrupted by user"
		return st
	}

	start := lastMarkerIdx(s.Turns)
	if call, ok := pendingCall(s.Turns, start); ok && !isQuestionTool(call.ToolName) {
		st.Text = "stopped during tool call " + call.ToolName
		st.PendingToolName = call.ToolName
		st.PendingToolInput = truncate(call.Input, maxPendingInput)
		return st
	}

	if info.MidTurn {
		st.Text = "stopped mid-turn"
		return st
	}

	if info.HasFinish {
		if text := finishText(s.Turns, info.Finish); text != "" {
			st.Text = text
			return st
		}
	}

	if lastIs(s.Turns, UserText) {
		st.Text = "user message not answered yet"
		return st
	}

	if pendingQuestion(s.Turns) {
		if call, ok := lastToolCall(s.Turns); ok {
			st.PendingToolName = call.ToolName
			st.PendingToolInput = truncate(call.Input, maxPendingInput)
		}
		st.Text = "waiting for user answer"
		return st
	}

	st.Text = "turn completed"
	return st
}

func lastIs(turns []Turn, kind TurnKind) bool {
	return len(turns) > 0 && turns[len(turns)-1].Kind == kind
}

func lastTurnText(turns []Turn, kind TurnKind) string {
	for _, turn := range slices.Backward(turns) {
		if turn.Kind == kind {
			return turn.Text
		}
	}
	return ""
}

// lastMarkerIdx is the index of the last UserText or Interrupted turn, the
// point after which a dangling ToolCall means "stopped mid-tool".
func lastMarkerIdx(turns []Turn) int {
	for i, turn := range slices.Backward(turns) {
		switch turn.Kind {
		case UserText, Interrupted:
			return i
		}
	}
	return -1
}

// pendingCall returns the last ToolCall after start that has no matching
// ToolResult. Calls before start are orphans left by earlier interruptions
// and are ignored.
func pendingCall(turns []Turn, start int) (Turn, bool) {
	var open []Turn
	for i := start + 1; i < len(turns); i++ {
		switch turns[i].Kind {
		case ToolCall:
			open = append(open, turns[i])
		case ToolResult:
			for j, o := range slices.Backward(open) {
				if o.ToolID == turns[i].ToolID {
					open = slices.Delete(open, j, j+1)
					break
				}
			}
		}
	}
	if len(open) == 0 {
		return Turn{}, false
	}
	return open[len(open)-1], true
}

func lastToolCall(turns []Turn) (Turn, bool) {
	for _, turn := range slices.Backward(turns) {
		if turn.Kind == ToolCall {
			return turn, true
		}
	}
	return Turn{}, false
}

func finishText(turns []Turn, finish string) string {
	switch finish {
	case "error":
		return "assistant turn ended with error"
	case "length":
		return "assistant turn ended with length"
	case "tool-calls":
		if quietAfterResults(turns) {
			return "stopped after tool results"
		}
		return ""
	default:
		if finish == "" {
			return "assistant turn incomplete"
		}
		return ""
	}
}

// quietAfterResults reports that no assistant text followed the last tool
// result, so the turn really ended on the tool exchange.
func quietAfterResults(turns []Turn) bool {
	lastResult := -1
	for i, t := range turns {
		if t.Kind == ToolResult {
			lastResult = i
		}
	}
	if lastResult < 0 {
		return false
	}
	for _, t := range turns[lastResult+1:] {
		if t.Kind == AssistantText {
			return false
		}
	}
	return true
}

func isQuestionTool(name string) bool {
	_, ok := questionTools[name]
	return ok
}

// pendingQuestion reports an open question: a question tool call that nothing
// has followed (no result, no reply, no further work), or a trailing
// assistant question the user has not reacted to.
func pendingQuestion(turns []Turn) bool {
	if call, ok := lastToolCall(turns); ok && isQuestionTool(call.ToolName) && quietAfter(turns) {
		return true
	}
	if st := lastTurnText(turns, AssistantText); st != "" && strings.HasSuffix(strings.TrimRight(st, " \t\n"), questionSuffix) {
		return quietAfterAssistant(turns)
	}
	return false
}

// quietAfter reports that after the last question tool call nothing happened:
// no result, no user or assistant text, no further calls.
func quietAfter(turns []Turn) bool {
	idx := -1
	for i, t := range turns {
		if t.Kind == ToolCall {
			idx = i
		}
	}
	return inactiveAfter(turns, idx)
}

func quietAfterAssistant(turns []Turn) bool {
	idx := -1
	for i, t := range turns {
		if t.Kind == AssistantText {
			idx = i
		}
	}
	return inactiveAfter(turns, idx)
}

func inactiveAfter(turns []Turn, idx int) bool {
	if idx < 0 {
		return false
	}
	for _, t := range turns[idx+1:] {
		switch t.Kind {
		case Reasoning, CompactionSummary:
			continue
		default:
			return false
		}
	}
	return true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
