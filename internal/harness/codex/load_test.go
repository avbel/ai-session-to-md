package codex

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

func kindsOf(turns []session.Turn) []session.TurnKind {
	out := make([]session.TurnKind, len(turns))
	for i, t := range turns {
		out[i] = t.Kind
	}
	return out
}

func metaByID(t *testing.T, a *Adapter, id string, includeArchived bool) session.Meta {
	t.Helper()
	metas, err := a.List(t.Context(), listOpts(includeArchived, 0))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(metas, func(m session.Meta) bool { return m.ID == id })
	if i < 0 {
		t.Fatalf("session %s not listed", id)
	}
	return metas[i]
}

func TestLoadFullSession(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	s, err := a.Load(t.Context(), metaByID(t, a, idFull, false))
	if err != nil {
		t.Fatal(err)
	}

	wantKinds := []session.TurnKind{
		session.UserText, session.UserText, session.UserText, session.AssistantText,
		session.AssistantText, session.Reasoning,
		session.ToolCall, session.ToolCall, session.ToolResult,
		session.ToolCall, session.ToolResult,
		session.ToolCall, session.ToolResult,
		session.ToolCall, session.ToolResult,
		session.CompactionSummary,
		session.UserText, session.AssistantText,
	}
	if !slices.Equal(kindsOf(s.Turns), wantKinds) {
		t.Fatalf("turn kinds = %v, want %v", kindsOf(s.Turns), wantKinds)
	}

	// injected texts are skipped; the unknown-tag one is logged but kept
	if got := s.Turns[0].Text; !strings.HasPrefix(got, "<guardian_tool_descriptions>") {
		t.Fatalf("unknown-tag turn = %q", got)
	}
	if got := s.Turns[1].Text; got != "Please fix the bug" {
		t.Fatalf("first user text = %q", got)
	}
	if got := s.Turns[2].Text; got != "[image]" {
		t.Fatalf("image turn = %q", got)
	}
	if got := s.Turns[3].Text; got != "I will fix it now." {
		t.Fatalf("assistant text = %q", got)
	}
	if got := s.Turns[4].Text; got != "[to reviewer] review please" {
		t.Fatalf("agent_message turn = %q", got)
	}
	if got := s.Turns[5].Text; got != "thinking hard\nmore" {
		t.Fatalf("reasoning turn = %q", got)
	}

	// call/result pairing by id, and through the output shapes
	if s.Turns[6].ToolName != "update_plan" || s.Turns[6].ToolID != "callPlan" {
		t.Fatalf("update_plan call wrong: %+v", s.Turns[6])
	}
	if s.Turns[7].ToolName != "apply_patch" {
		t.Fatalf("apply_patch call wrong: %+v", s.Turns[7])
	}
	if s.Turns[8].ToolID != "callA" || s.Turns[8].Text != "patch applied" {
		t.Fatalf("string output wrong: %+v", s.Turns[8])
	}
	if s.Turns[10].ToolID != "callB" || s.Turns[10].Text != "file1\nfile2" {
		t.Fatalf("array output wrong: %+v", s.Turns[10])
	}
	if s.Turns[12].ToolID != "callC" || s.Turns[12].Text != "all passed" {
		t.Fatalf("object output wrong: %+v", s.Turns[12])
	}

	// null call_id on both sides pairs with the most recent unpaired call
	if s.Turns[13].ToolID == "" || s.Turns[13].ToolID != s.Turns[14].ToolID {
		t.Fatalf("null call_id pairing failed: %q vs %q", s.Turns[13].ToolID, s.Turns[14].ToolID)
	}
	if s.Turns[14].Text != "probe result" {
		t.Fatalf("null-paired output text = %q", s.Turns[14].Text)
	}

	if got := s.Turns[15].Text; got != "earlier context summarized" {
		t.Fatalf("compaction text = %q", got)
	}
	if got := s.Turns[16].Text; got != "continue please" {
		t.Fatalf("late user text = %q", got)
	}
	if got := s.Turns[17].Text; got != "Done. All tests pass." {
		t.Fatalf("late assistant text = %q", got)
	}

	if s.Model != "gpt-5.2" || s.HarnessVer != "0.42.0" {
		t.Fatalf("model/version wrong: %q %q", s.Model, s.HarnessVer)
	}
	if s.Cwd != "/tmp/proj" {
		t.Fatalf("cwd = %q", s.Cwd)
	}
	if s.RecordedGit.RepoURL != "https://github.com/example/one.git" ||
		s.RecordedGit.Branch != "main" || s.RecordedGit.Commit != "abc123def" {
		t.Fatalf("recorded git wrong: %+v", s.RecordedGit)
	}

	wantTodos := []session.Todo{
		{Content: "write tests", Status: "in_progress"},
		{Content: "ship", Status: "pending"},
	}
	if !slices.Equal(s.Todos, wantTodos) {
		t.Fatalf("todos = %+v, want %+v", s.Todos, wantTodos)
	}

	wantFiles := []string{"internal/a.go", "internal/new.go", "internal/old.go"}
	if !slices.Equal(s.FilesTouched, wantFiles) {
		t.Fatalf("files touched = %v, want %v", s.FilesTouched, wantFiles)
	}

	if s.Skipped != 2 { // malformed line + unknown world_state type
		t.Fatalf("skipped = %d, want 2", s.Skipped)
	}
}

func TestLoadFullSessionStatusTurnCompleted(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	s, err := a.Load(t.Context(), metaByID(t, a, idFull, false))
	if err != nil {
		t.Fatal(err)
	}
	if st := a.DeriveStatus(s); st.Text != "turn completed" {
		t.Fatalf("status = %q, want turn completed", st.Text)
	}
	if st := a.DeriveStatus(s); st.MidTurn {
		t.Fatal("task_complete must clear mid-turn")
	}
}

func TestLoadMidTurnWithoutComplete(t *testing.T) {
	dir := t.TempDir()
	id := "bbbb0000-mid"
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "02", "02",
		"rollout-2026-02-02T09-00-00-"+id+".jsonl"), midTurnLines())

	a := testAdapter(dir, nil)
	metas, err := a.List(t.Context(), listOpts(false, 0))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list mid fixture: %v %v", err, metas)
	}
	s, err := a.Load(t.Context(), metas[0])
	if err != nil {
		t.Fatal(err)
	}

	if st := a.DeriveStatus(s); st.Text != "stopped mid-turn" {
		t.Fatalf("status = %q, want stopped mid-turn", st.Text)
	}
	wantFiles := []string{"/tmp/proj/a.go", "/tmp/proj/b.go", "c.txt"}
	if !slices.Equal(s.FilesTouched, wantFiles) {
		t.Fatalf("FileChange files = %v, want %v", s.FilesTouched, wantFiles)
	}
}

// midTurnLines ends with task_started and no task_complete.
func midTurnLines() []string {
	const ts = "2026-02-02T09:00:00Z"
	return []string{
		rolloutLine(ts, "session_meta", metaPayloadMap("/tmp/proj")),
		rolloutLine(ts, "turn_context", map[string]any{"model": "gpt-5.2", "cwd": "/tmp/proj"}),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", "start the work"))),
		rolloutLine(ts, "event_msg", map[string]any{"type": "task_started"}),
		rolloutLine(ts, "event_msg", map[string]any{"type": "item_completed",
			"item": map[string]any{"type": "FileChange", "changes": map[string]any{
				"/tmp/proj/a.go": map[string]any{}, "/tmp/proj/b.go": map[string]any{}}}}),
		rolloutLine(ts, "event_msg", map[string]any{"type": "item_completed",
			"item": map[string]any{"type": "FileChange",
				"changes": []any{map[string]any{"path": "c.txt"}}}}),
		rolloutLine(ts, "response_item", assistantMessage(textItem("output_text", "still working..."))),
	}
}

func TestLoadTurnAborted(t *testing.T) {
	dir := t.TempDir()
	id := "cccc0000-abrt"
	const ts = "2026-02-03T09:00:00Z"
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "02", "03",
		"rollout-2026-02-03T09-00-00-"+id+".jsonl"), []string{
		rolloutLine(ts, "session_meta", metaPayloadMap("/tmp/proj")),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", "do it"))),
		rolloutLine(ts, "event_msg", map[string]any{"type": "task_started"}),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", "<turn_aborted reason=stop>"))),
		rolloutLine(ts, "event_msg", map[string]any{"type": "turn_aborted"}),
	})

	a := testAdapter(dir, nil)
	metas, err := a.List(t.Context(), listOpts(false, 0))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list aborted fixture: %v %v", err, metas)
	}
	s, err := a.Load(t.Context(), metas[0])
	if err != nil {
		t.Fatal(err)
	}
	want := []session.TurnKind{session.UserText, session.Interrupted, session.Interrupted}
	if !slices.Equal(kindsOf(s.Turns), want) {
		t.Fatalf("aborted turns = %v, want %v", kindsOf(s.Turns), want)
	}
	if st := a.DeriveStatus(s); st.Text != "interrupted by user" {
		t.Fatalf("status = %q, want interrupted by user", st.Text)
	}
}

func TestLoadTurnContextKeepsLast(t *testing.T) {
	dir := t.TempDir()
	const ts = "2026-02-04T09:00:00Z"
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "02", "04",
		"rollout-2026-02-04T09-00-00-dddd0000-ctx.jsonl"), []string{
		rolloutLine(ts, "session_meta", metaPayloadMap("/old/cwd")),
		rolloutLine(ts, "turn_context", map[string]any{"model": "m1", "cwd": "/old/cwd"}),
		rolloutLine(ts, "turn_context", map[string]any{"model": "m2", "cwd": "/new/cwd"}),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", "hi"))),
		rolloutLine(ts, "compacted", map[string]any{"message": "compressed earlier"}),
	})

	a := testAdapter(dir, nil)
	metas, err := a.List(t.Context(), listOpts(false, 0))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list ctx fixture: %v %v", err, metas)
	}
	s, err := a.Load(t.Context(), metas[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "m2" || s.Cwd != "/new/cwd" {
		t.Fatalf("keep-last violated: model=%q cwd=%q", s.Model, s.Cwd)
	}
	want := []session.TurnKind{session.UserText, session.CompactionSummary}
	if !slices.Equal(kindsOf(s.Turns), want) {
		t.Fatalf("turns = %v, want %v", kindsOf(s.Turns), want)
	}
	if got := s.Turns[1].Text; got != "compressed earlier" {
		t.Fatalf("compaction text = %q", got)
	}
}

func TestClassifyUserTextInjectedPrefixes(t *testing.T) {
	cases := []struct {
		text string
		want session.TurnKind
		ok   bool
	}{
		{"# AGENTS.md instructions\nrule", 0, false},
		{"You are in a git worktree /tmp/x", 0, false},
		{"<environment_context>\ncwd=/x", 0, false},
		{"<user_instructions>obey</user_instructions>", 0, false},
		{"<INSTRUCTIONS_9>obey", 0, false},
		{"<skill>web-dev</skill>", 0, false},
		{"<permissions>none", 0, false},
		{"<collaboration_mode>pair", 0, false},
		{"<cwd>/tmp/x", 0, false},
		{"<external_codex_apps_open_page>1", 0, false},
		{"<current_foo>1", 0, false},
		{"<send_user_message_question_reply>ok", 0, false},
		{"<turn_aborted reason=stop>", session.Interrupted, true},
		{"Please fix the bug", session.UserText, true},
	}
	for _, c := range cases {
		turn, ok := classifyUserText(c.text, func(string, ...any) {})
		if ok != c.ok || (ok && turn.Kind != c.want) {
			t.Errorf("classifyUserText(%q) = (%v, %v), want kind %v ok %v",
				c.text, turn.Kind, ok, c.want, c.ok)
		}
	}

	var logs []string
	_, _ = classifyUserText("<guardian_tool_descriptions>\nhidden", func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	if len(logs) != 1 || !strings.Contains(logs[0], "unknown injected tag") {
		t.Fatalf("unknown tag not logged: %v", logs)
	}
	_, _ = classifyUserText("", func(string, ...any) {})
}

func TestPatchPaths(t *testing.T) {
	raw := "*** Begin Patch\n*** Update File: internal/a.go\n*** Add File: new.txt\n*** Delete File: old.txt\n*** Move to: moved.txt\n"
	got := patchPaths("apply_patch", raw)
	want := []string{"internal/a.go", "new.txt", "old.txt", "moved.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("raw patch paths = %v, want %v", got, want)
	}

	wrapped := `{"input":"*** Begin Patch\n*** Update File: x/y.ts\n","summary":"edit"}`
	got = patchPaths("shell", wrapped)
	if !slices.Equal(got, []string{"x/y.ts"}) {
		t.Fatalf("wrapped patch paths = %v", got)
	}

	if got := patchPaths("shell", "ls -la"); got != nil {
		t.Fatalf("non-patch input produced %v", got)
	}

	if got := patchPaths("shell", "*** Begin Patch\n*** Update File: only.json\n"); !slices.Equal(got, []string{"only.json"}) {
		t.Fatalf("begin-patch-without-apply_patch = %v", got)
	}
}
