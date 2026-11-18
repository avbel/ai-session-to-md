package session

import (
	"strings"
	"testing"
)

func TestIsMetaRequest(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Remember what we did", true},
		{"remember what we've done!", true},
		{"  please summarize this session.  ", true},
		{"Save progress", true},
		{"write a handoff for codex", true},
		{"/compact", true},
		{"/compact keep the test notes", true},
		{"/save-session", true},
		{"fix the bug", false},
		{"summarize the errors in reduce.go", false},
		{"/compactor", false},
		{"", false},
		{"remember what we did " + strings.Repeat("and then refactor the reader ", 5), false},
	}
	for _, c := range cases {
		if got := IsMetaRequest(c.text); got != c.want {
			t.Errorf("IsMetaRequest(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestDeriveSkipsTrailingMetaRequest(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "add retries to the client"),
		call("c1", "Edit", `{"file_path":"client.go"}`),
		result("c1", "ok"),
		txt(AssistantText, "retries added"),
		txt(UserText, "Remember what we did"),
		txt(AssistantText, "We added retries to the client."),
	}}
	st := Derive(s, Info{})
	if st.LastUser != "add retries to the client" {
		t.Fatalf("last user %q", st.LastUser)
	}
	if st.LastAssistant != "retries added" {
		t.Fatalf("last assistant %q", st.LastAssistant)
	}
	if st.Text != "turn completed" {
		t.Fatalf("status %q", st.Text)
	}
}

func TestDeriveSkipsConsecutiveMetaRequests(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "port the parser"),
		txt(AssistantText, "ported"),
		txt(UserText, "/compact"),
		txt(UserText, "save progress"),
	}}
	st := Derive(s, Info{})
	if st.LastUser != "port the parser" {
		t.Fatalf("last user %q", st.LastUser)
	}
	if st.Text != "user message not answered yet" {
		t.Fatalf("status %q", st.Text)
	}
}

func TestDeriveKeepsOnlyMetaRequest(t *testing.T) {
	s := Session{Turns: []Turn{
		txt(UserText, "remember what we did"),
		txt(AssistantText, "nothing yet"),
	}}
	st := Derive(s, Info{})
	if st.LastUser != "remember what we did" || st.LastAssistant != "nothing yet" {
		t.Fatalf("got %q / %q", st.LastUser, st.LastAssistant)
	}
}
