package opencode

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// runStatus loads the fixture with a given finish variant on the last
// assistant message and derives the status.
func runStatus(t *testing.T, finish string, content []any) (session.Status, session.Info) {
	t.Helper()
	_, a := fixtureDB(t, func() map[string]any {
		m := map[string]any{"content": content}
		if finish != "" {
			m["finish"] = finish
		}
		return m
	})
	s, info := mustLoad(t, a, "ses_fix01")
	return session.Derive(s, info), info
}

func TestStatusPendingTool(t *testing.T) {
	st, _ := runStatus(t, "", []any{
		toolItem("t2", "write", "pending", map[string]any{"filePath": "/proj/x.go"}, nil),
	})
	want := "stopped during tool call write"
	if st.Text != want {
		t.Fatalf("status = %q, want %q", st.Text, want)
	}
	if st.PendingToolName != "write" || !strings.Contains(st.PendingToolInput, "/proj/x.go") {
		t.Fatalf("pending tool = %q %q", st.PendingToolName, st.PendingToolInput)
	}
}

func TestStatusFinishError(t *testing.T) {
	st, _ := runStatus(t, "error", []any{})
	if st.Text != "assistant turn ended with error" {
		t.Fatalf("status = %q", st.Text)
	}
}

func TestStatusFinishLength(t *testing.T) {
	st, _ := runStatus(t, "length", []any{})
	if st.Text != "assistant turn ended with length" {
		t.Fatalf("status = %q", st.Text)
	}
}

func TestStatusFinishMissingIncomplete(t *testing.T) {
	st, _ := runStatus(t, "", []any{map[string]any{"type": "text", "text": "done"}})
	if st.Text != "assistant turn incomplete" {
		t.Fatalf("status = %q", st.Text)
	}
}

func TestStatusFinishToolCalls(t *testing.T) {
	st, _ := runStatus(t, "tool-calls", []any{
		toolItem("t3", "read", "completed", map[string]any{"path": "x"}, "data"),
	})
	if st.Text != "stopped after tool results" {
		t.Fatalf("status = %q", st.Text)
	}
}

func TestStatusFinishStopCompleted(t *testing.T) {
	st, _ := runStatus(t, "stop", []any{map[string]any{"type": "text", "text": "all done"}})
	if st.Text != "turn completed" {
		t.Fatalf("status = %q", st.Text)
	}
}

func TestStatusUserNotAnswered(t *testing.T) {
	// No assistant messages at all: no finish information exists, the last
	// turn is the user message.
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_justusr", "/proj", "u", "", "v1", 1763000100000, nil)
	insertMsg(t, db, "ses_justusr", 1, "user", map[string]any{"text": "hello"}, 1)
	a := New(filepath.Dir(path))
	s, info := mustLoad(t, a, "ses_justusr")
	st := session.Derive(s, info)
	if info.HasFinish {
		t.Fatal("HasFinish = true, want false without assistant messages")
	}
	if st.Text != "user message not answered yet" {
		t.Fatalf("status = %q", st.Text)
	}
}

func TestTodosFromToolFallback(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_tdonly1", "/proj", "td", "", "v1", 1763000100000, nil)
	// No todo rows: the last todowrite input wins.
	insertMsg(t, db, "ses_tdonly1", 1, "assistant", map[string]any{
		"content": []any{
			toolItem("t9", "todowrite", "completed",
				map[string]any{"todos": []any{
					map[string]any{"content": "write tests", "status": "completed"},
					map[string]any{"content": "ship it", "status": "in_progress"},
				}}, nil),
		},
	}, 1)
	a := New(filepath.Dir(path))
	s, _ := mustLoad(t, a, "ses_tdonly1")
	if len(s.Todos) != 2 || s.Todos[0].Content != "write tests" || s.Todos[1].Status != "in_progress" {
		t.Fatalf("todos = %+v", s.Todos)
	}
}

func TestSkippedCounts(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_skippy1", "/proj", "sk", "", "v1", 1763000100000, nil)
	insertRawMsg(t, db, "ses_skippy1", 1, "user", "{not json")
	insertMsg(t, db, "ses_skippy1", 2, "brand-new-type", map[string]any{"text": "x"}, 2)
	a := New(filepath.Dir(path))
	s, _ := mustLoad(t, a, "ses_skippy1")
	if s.Skipped != 2 {
		t.Fatalf("Skipped = %d, want 2 (bad JSON + unknown type)", s.Skipped)
	}
	if len(s.Turns) != 0 {
		t.Fatalf("turns = %d, want 0", len(s.Turns))
	}
}

func TestPatchFilesTouched(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_patchy1", "/proj", "p", "", "v1", 1763000100000, nil)
	insertMsg(t, db, "ses_patchy1", 1, "assistant", map[string]any{
		"content": []any{
			toolItem("t1", "apply_patch", "completed", map[string]any{
				"patchText": "*** Begin Patch\n*** Add File: new.go\n+code\n*** Update File: old.go\n*** Delete File: gone.txt\n*** Move to: renamed.txt\n*** End Patch",
			}, "ok"),
			toolItem("t2", "edit", "completed", map[string]any{"filePath": "/proj/main.go"}, "ok"),
			toolItem("t3", "bash", "completed", map[string]any{"command": "ls"}, "ok"),
		},
	}, 1)
	a := New(filepath.Dir(path))
	s, _ := mustLoad(t, a, "ses_patchy1")
	want := []string{"new.go", "old.go", "gone.txt", "renamed.txt", "/proj/main.go"}
	if len(s.FilesTouched) != len(want) {
		t.Fatalf("files = %v, want %v", s.FilesTouched, want)
	}
	for i, w := range want {
		if s.FilesTouched[i] != w {
			t.Fatalf("files[%d] = %q, want %q", i, s.FilesTouched[i], w)
		}
	}
}

func TestModelResolution(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_modely1", "/proj", "m", `{"id":"gpt-5","providerID":"openai"}`, "v1", 1763000100000, nil)
	insertMsg(t, db, "ses_modely1", 1, "assistant", map[string]any{
		"content": []any{},
	}, 1)
	a := New(filepath.Dir(path))
	s, _ := mustLoad(t, a, "ses_modely1")
	if s.Model != "openai/gpt-5" {
		t.Fatalf("model = %q, want openai/gpt-5", s.Model)
	}
}
