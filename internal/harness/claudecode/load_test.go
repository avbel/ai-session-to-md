package claudecode

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

func loadFixture(t *testing.T, id string) session.Session {
	t.Helper()
	a := newFixtureAdapter(t)
	s, err := a.Load(context.Background(), session.Meta{Harness: session.HarnessClaudeCode, ID: id})
	if err != nil {
		t.Fatalf("Load %s: %v", id, err)
	}
	return s
}

type turnSig struct {
	Kind  session.TurnKind
	Text  string
	Name  string
	ID    string
	Input string
	IsErr bool
}

func signatures(t *testing.T, turns []session.Turn) []turnSig {
	t.Helper()
	out := make([]turnSig, 0, len(turns))
	for _, turn := range turns {
		out = append(out, turnSig{turn.Kind, turn.Text, turn.ToolName, turn.ToolID, turn.Input, turn.IsError})
	}
	return out
}

func wantSigs(t *testing.T, got []turnSig, want []turnSig) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d turns, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Text != w.Text || g.Name != w.Name ||
			g.ID != w.ID || g.Input != w.Input || g.IsErr != w.IsErr {
			t.Errorf("turn[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestLoadPlain(t *testing.T) {
	s := loadFixture(t, "plain")
	if s.Title != "Fix the failing test in internal/reduce" {
		t.Errorf("Title = %q", s.Title)
	}
	if s.Cwd != "/tmp/proj/sub" {
		t.Errorf("Cwd = %q, want last cwd", s.Cwd)
	}
	if s.Branch != "feature" {
		t.Errorf("Branch = %q, want last gitBranch", s.Branch)
	}
	if s.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.HarnessVer != "1.2.3" {
		t.Errorf("HarnessVer = %q", s.HarnessVer)
	}
	if s.Skipped != 0 {
		t.Errorf("Skipped = %d", s.Skipped)
	}
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.UserText, "Fix the failing test in internal/reduce", "", "", "", false},
		{session.AssistantText, "On it.", "", "", "", false},
		{session.ToolCall, "", "Bash", "tu_1", `{"command":"go test ./..."}`, false},
		{session.ToolResult, "ok\nFAIL TestReduce", "", "tu_1", "", false},
		{session.Reasoning, "the test asserts the old marker", "", "", "", false},
		{session.ToolCall, "", "Edit", "tu_2", `{"file_path":"/tmp/proj/internal/reduce/reduce.go"}`, false},
		{session.ToolCall, "", "TodoWrite", "tu_3", `{"todos":[{"content":"fix the test","status":"in_progress"},{"content":"rerun go test","status":"pending"}]}`, false},
		{session.ToolResult, "file edited", "", "tu_2", "", false},
		{session.ToolCall, "", "TaskCreate", "tu_4", `{"subject":"write the handoff"}`, false},
		{session.AssistantText, "Done for now.", "", "", "", false},
		{session.ToolResult, "Todos updated", "", "tu_3", "", false},
		{session.ToolResult, "Created task #7: write the handoff", "", "tu_4", "", false},
	})
	first := s.Turns[0]
	want := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	if !first.Time.Equal(want) {
		t.Errorf("turn[0].Time = %v, want %v", first.Time, want)
	}
	wantTodos := []session.Todo{
		{Content: "fix the test", Status: "in_progress"},
		{Content: "rerun go test", Status: "pending"},
		{Content: "write the handoff", Status: "pending"},
	}
	if len(s.Todos) != len(wantTodos) {
		t.Fatalf("Todos = %+v, want %+v", s.Todos, wantTodos)
	}
	for i := range wantTodos {
		if s.Todos[i] != wantTodos[i] {
			t.Errorf("Todos[%d] = %+v, want %+v", i, s.Todos[i], wantTodos[i])
		}
	}
	if len(s.FilesTouched) != 1 || s.FilesTouched[0] != "/tmp/proj/internal/reduce/reduce.go" {
		t.Errorf("FilesTouched = %v", s.FilesTouched)
	}
}

func TestLoadRewind(t *testing.T) {
	s := loadFixture(t, "rewind")
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.UserText, "first attempt", "", "", "", false},
		{session.UserText, "redo it properly", "", "", "", false},
		{session.AssistantText, "better answer", "", "", "", false},
		{session.UserText, "thanks", "", "", "", false},
		{session.AssistantText, "sure", "", "", "", false},
	})
	for _, turn := range s.Turns {
		if turn.Text == "wrong answer" {
			t.Error("discarded branch leaked into turns")
		}
	}
}

func TestLoadCompact(t *testing.T) {
	s := loadFixture(t, "compact")
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.UserText, "start the work", "", "", "", false},
		{session.AssistantText, "working on it", "", "", "", false},
		{session.CompactionSummary, "Summary: earlier exploration condensed into notes", "", "", "", false},
		{session.AssistantText, "continuing from the summary", "", "", "", false},
		{session.UserText, "go on", "", "", "", false},
	})
}

func TestLoadSidechain(t *testing.T) {
	s := loadFixture(t, "sidechain")
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.UserText, "explore the module", "", "", "", false},
		{session.ToolCall, "", "Task", "tu_1", `{"prompt":"explore x"}`, false},
		{session.UserText, "now the main thread", "", "", "", false},
		{session.AssistantText, "main answer", "", "", "", false},
	})
	for _, turn := range s.Turns {
		if turn.Text == "subagent prompt" {
			t.Error("sidechain entry leaked into turns")
		}
	}
	if len(s.FilesTouched) != 0 {
		t.Errorf("FilesTouched = %v, want sidechain edits excluded", s.FilesTouched)
	}
}

func TestLoadNoisy(t *testing.T) {
	s := loadFixture(t, "noisy")
	if s.Title != "Custom title" {
		t.Errorf("Title = %q", s.Title)
	}
	if s.Model != "m1" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.HarnessVer != "9.9.9" {
		t.Errorf("HarnessVer = %q", s.HarnessVer)
	}
	if s.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", s.Skipped)
	}
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.UserText, "/model sonnet", "", "", "", false},
		{session.AssistantText, "working on it", "", "", "", false},
		{session.Interrupted, "", "", "", "", false},
		{session.UserText, "Continue please", "", "", "", false},
	})
}

func TestLoadTasks(t *testing.T) {
	s := loadFixture(t, "tasks")
	wantTodos := []session.Todo{
		{Content: "write the changelog", Status: "completed"},
		{Content: "tag the release", Status: "pending"},
	}
	if len(s.Todos) != len(wantTodos) {
		t.Fatalf("Todos = %+v, want %+v", s.Todos, wantTodos)
	}
	for i := range wantTodos {
		if s.Todos[i] != wantTodos[i] {
			t.Errorf("Todos[%d] = %+v, want %+v", i, s.Todos[i], wantTodos[i])
		}
	}
	if len(s.FilesTouched) != 1 || s.FilesTouched[0] != "/tmp/proj/analysis.ipynb" {
		t.Errorf("FilesTouched = %v", s.FilesTouched)
	}
	if s.Branch != "dev" || s.HarnessVer != "2.0.0" {
		t.Errorf("Branch = %q, HarnessVer = %q", s.Branch, s.HarnessVer)
	}
}

func TestLoadTodoReset(t *testing.T) {
	s := loadFixture(t, "todo-reset")
	if len(s.Todos) != 1 || s.Todos[0] != (session.Todo{Content: "fresh todo", Status: "pending"}) {
		t.Fatalf("Todos = %+v, want fresh list only", s.Todos)
	}
}

func TestLoadChainCycle(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-tmp-cycle")
	mkdir(t, proj)
	write(t, filepath.Join(proj, "cyc.jsonl"), strings.Join([]string{
		userLine("u1", `"begin"`),
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"text","text":"mid"}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","message":{"role":"user","content":"next"}}`,
		`{"type":"user","uuid":"u3","parentUuid":"c1","message":{"role":"user","content":"after cycle"}}`,
		`{"type":"assistant","uuid":"c1","parentUuid":"c2","message":{"role":"assistant","content":[{"type":"text","text":"c1"}]}}`,
		`{"type":"assistant","uuid":"c2","parentUuid":"c1","message":{"role":"assistant","content":[{"type":"text","text":"c2"}]}}`,
	}, "\n")+"\n")
	a := New(dir)
	s, err := a.Load(context.Background(), session.Meta{ID: "cyc"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Leaf c2 sits inside the c1<->c2 cycle; the visited guard stops the
	// walk there instead of looping forever.
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.AssistantText, "c1", "", "", "", false},
		{session.AssistantText, "c2", "", "", "", false},
	})
}

func TestLoadHugeLine(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-tmp-huge")
	mkdir(t, proj)
	big := strings.Repeat("x", 2<<20)
	write(t, filepath.Join(proj, "huge.jsonl"), strings.Join([]string{
		userLine("u1", `"start"`),
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","model":"m1","content":[{"type":"text","text":"` + big + `"}]}}`,
	}, "\n")+"\n")
	a := New(dir)
	metas, err := a.List(context.Background(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].Title != "start" {
		t.Fatalf("metas = %+v", metas)
	}
	s, err := a.Load(context.Background(), session.Meta{ID: "huge"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Skipped != 0 {
		t.Errorf("Skipped = %d", s.Skipped)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(s.Turns))
	}
	if got := s.Turns[1].Text; len(got) != 2<<20 || got != big {
		t.Errorf("huge text: len = %d, equal = %v", len(got), got == big)
	}
}

func TestLoadChainFallback(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-tmp-fallback")
	mkdir(t, proj)
	write(t, filepath.Join(proj, "fb.jsonl"), strings.Join([]string{
		userLine("u1", `"first"`),
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"text","text":"mid"}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"uuid-not-in-file","message":{"role":"user","content":"orphan"}}`,
	}, "\n")+"\n")
	a := New(dir)
	s, err := a.Load(context.Background(), session.Meta{ID: "fb"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantSigs(t, signatures(t, s.Turns), []turnSig{
		{session.UserText, "first", "", "", "", false},
		{session.AssistantText, "mid", "", "", "", false},
		{session.UserText, "orphan", "", "", "", false},
	})
}

func TestLoadResolveErrors(t *testing.T) {
	a := newFixtureAdapter(t)
	if _, err := a.Load(context.Background(), session.Meta{ID: "does-not-exist"}); err == nil {
		t.Error("unknown id: want error")
	}
	if _, err := a.Load(context.Background(), session.Meta{}); err == nil {
		t.Error("empty id: want error")
	}
	if _, err := a.Load(context.Background(), session.Meta{ID: "../plain"}); err == nil {
		t.Error("pathy id: want error")
	}
}
