package session

import (
	"strings"
	"testing"
)

func txt(kind TurnKind, s string) Turn {
	return Turn{Kind: kind, Text: s}
}

func call(id, name, input string) Turn {
	return Turn{Kind: ToolCall, ToolID: id, ToolName: name, Input: input}
}

func result(id, s string) Turn {
	return Turn{Kind: ToolResult, ToolID: id, Text: s}
}

func TestDeriveInterrupted(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "fix the bug"),
		txt(AssistantText, "on it"),
		txt(Interrupted, "[Request interrupted by user]"),
	}}
	st := Derive(s, Info{})
	if st.Text != "interrupted by user" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveInterruptedWinsOverPendingCall(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "Bash", "{}"),
		txt(Interrupted, ""),
	}}
	st := Derive(s, Info{})
	if st.Text != "interrupted by user" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDerivePendingToolCall(t *testing.T) {
	input := strings.Repeat("x", 1600)
	s := Session{Turns: []Turn{
		txt(UserText, "run the tests"),
		call("c1", "Bash", input),
	}}
	st := Derive(s, Info{})
	if st.Text != "stopped during tool call Bash" {
		t.Fatalf("got %q", st.Text)
	}
	if st.PendingToolName != "Bash" {
		t.Fatalf("pending name %q", st.PendingToolName)
	}
	if len(st.PendingToolInput) != 1500 {
		t.Fatalf("pending input len %d, want 1500", len(st.PendingToolInput))
	}
	if !strings.HasPrefix(input, st.PendingToolInput) {
		t.Fatal("pending input is not a prefix of the original")
	}
}

func TestDerivePendingToolCallPicksLastUnpaired(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "Read", "{}"),
		result("c1", "ok"),
		call("c2", "Write", "{}"),
		call("c3", "Edit", "{}"),
		result("c3", "done"),
	}}
	st := Derive(s, Info{})
	if st.Text != "stopped during tool call Write" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveOrphanedCallIgnored(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "first task"),
		call("c1", "Bash", "{}"),
		txt(UserText, "actually do this instead"),
		txt(AssistantText, "ok"),
	}}
	st := Derive(s, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveOrphanedCallAfterInterruptIgnored(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "Bash", "{}"),
		txt(Interrupted, "[Request interrupted by user for tool use]"),
		txt(UserText, "skip that, do the other thing"),
		txt(AssistantText, "done"),
	}}
	st := Derive(s, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveMidTurn(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		txt(Reasoning, "thinking"),
	}}
	st := Derive(s, Info{MidTurn: true})
	if st.Text != "stopped mid-turn" {
		t.Fatalf("got %q", st.Text)
	}
	if !st.MidTurn {
		t.Fatal("MidTurn flag not set")
	}
}

func TestDeriveFinishError(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		txt(AssistantText, "partial"),
	}}
	st := Derive(s, Info{HasFinish: true, Finish: "error"})
	if st.Text != "assistant turn ended with error" {
		t.Fatalf("got %q", st.Text)
	}
	if st.Finish != "error" {
		t.Fatalf("finish %q", st.Finish)
	}
}

func TestDeriveFinishLength(t *testing.T) {
	s := Session{Turns: []Turn{txt(UserText, "go")}}
	st := Derive(s, Info{HasFinish: true, Finish: "length"})
	if st.Text != "assistant turn ended with length" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveFinishMissingIsIncomplete(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		txt(Reasoning, "halfway"),
	}}
	st := Derive(s, Info{HasFinish: true})
	if st.Text != "assistant turn incomplete" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveNoFinishInfoIsNotIncomplete(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "Bash", "{}"),
		result("c1", "ok"),
		txt(AssistantText, "done"),
	}}
	st := Derive(s, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveStoppedAfterToolResults(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "Bash", "{}"),
		result("c1", "all passed"),
	}}
	st := Derive(s, Info{HasFinish: true, Finish: "tool-calls"})
	if st.Text != "stopped after tool results" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveToolCallsFinishWithLaterTextContinues(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "Bash", "{}"),
		result("c1", "ok"),
		txt(AssistantText, "tests pass"),
	}}
	st := Derive(s, Info{HasFinish: true, Finish: "tool-calls"})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveFinishStopCompletes(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		txt(AssistantText, "all done"),
	}}
	st := Derive(s, Info{HasFinish: true, Finish: "stop"})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveUserNotAnswered(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(AssistantText, "earlier reply"),
		txt(UserText, "and now this"),
	}}
	st := Derive(s, Info{})
	if st.Text != "user message not answered yet" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveQuestionToolPending(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "refactor it"),
		txt(AssistantText, "which style?"),
		call("c1", "AskUserQuestion", `{"question":"which style?"}`),
	}}
	st := Derive(s, Info{})
	if st.Text != "waiting for user answer" {
		t.Fatalf("got %q", st.Text)
	}
	if st.PendingToolName != "AskUserQuestion" {
		t.Fatalf("pending name %q", st.PendingToolName)
	}
}

func TestDeriveQuestionToolOpenCodeName(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "request_user_input", "{}"),
	}}
	st := Derive(s, Info{})
	if st.Text != "waiting for user answer" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveQuestionToolAnsweredNotWaiting(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "go"),
		call("c1", "AskUserQuestion", "{}"),
		result("c1", "option A"),
		txt(AssistantText, "going with A"),
	}}
	st := Derive(s, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveTrailingQuestion(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "fix it"),
		txt(AssistantText, "Found two options. Want me to take the faster one?"),
	}}
	st := Derive(s, Info{})
	if st.Text != "waiting for user answer" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveTrailingQuestionThenWorkContinues(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(AssistantText, "shall I run the tests?"),
		call("c1", "Bash", "{}"),
		result("c1", "ok"),
		txt(AssistantText, "done"),
	}}
	st := Derive(s, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveCompleted(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "add the feature"),
		txt(Reasoning, "plan"),
		call("c1", "Edit", `{"file_path":"a.go"}`),
		result("c1", "ok"),
		txt(AssistantText, "shipped"),
		txt(CompactionSummary, "summary of older turns"),
	}}
	st := Derive(s, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
	if st.LastUser != "add the feature" {
		t.Fatalf("last user %q", st.LastUser)
	}
	if st.LastAssistant != "shipped" {
		t.Fatalf("last assistant %q", st.LastAssistant)
	}
}

func TestDeriveEmptySession(t *testing.T) {
	st := Derive(Session{}, Info{})
	if st.Text != "turn completed" {
		t.Fatalf("got %q", st.Text)
	}
}

func TestDeriveLastUserAndAssistantVerbatim(t *testing.T) {
	user := "line one\n\tindented  \nwith `ticks` and \"quotes\""
	assistant := "  keeps leading spaces\nand trailing\t"
	s := Session{Turns: []Turn{
		txt(UserText, "older"),
		txt(AssistantText, "older reply"),
		txt(UserText, user),
		txt(AssistantText, assistant),
	}}
	st := Derive(s, Info{})
	if st.LastUser != user {
		t.Fatalf("last user %q, want verbatim %q", st.LastUser, user)
	}
	if st.LastAssistant != assistant {
		t.Fatalf("last assistant %q, want verbatim %q", st.LastAssistant, assistant)
	}
}

func TestDeriveLastUserAbsent(t *testing.T) {
	s := Session{Turns: []Turn{txt(AssistantText, "only assistant")}}
	st := Derive(s, Info{})
	if st.LastUser != "" || st.LastAssistant != "only assistant" {
		t.Fatalf("got %q / %q", st.LastUser, st.LastAssistant)
	}
}

func TestTruncateMultibyte(t *testing.T) {
	s := strings.Repeat("é", 800)
	got := truncate(s, 1500)
	if got != s[:1500] {
		t.Fatalf("truncate cut mid-rune: %d bytes", len(got))
	}
}
