package codex

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// testdataFixturePath is the committed synthetic rollout documenting the
// §7.2 format; regenerated with `go test -update`.
const testdataFixturePath = "../../../testdata/codex/rollout-example.jsonl"

var update = flag.Bool("update", false, "rewrite testdata fixtures from the generators")

func TestUpdateTestdataFixture(t *testing.T) {
	if !*update {
		t.Skip("run with -update to rewrite testdata/codex/rollout-example.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(testdataFixturePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testdataFixturePath,
		[]byte(joinLines(fullRolloutLines())), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadTestdataFixture loads the committed fixture as a fallback-listed
// session and checks the headline facts end to end.
func TestLoadTestdataFixture(t *testing.T) {
	raw, err := os.ReadFile(testdataFixturePath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("fixture missing; run `go test ./internal/harness/codex -update` once: %v", err)
		}
		t.Fatal(err)
	}

	dir := t.TempDir()
	id := "sid"
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "11", "14",
		"rollout-2026-11-14T10-00-00-"+id+".jsonl"), strings.Split(strings.TrimRight(string(raw), "\n"), "\n"))

	a := testAdapter(dir, nil)
	s, err := a.Load(t.Context(), metaByID(t, a, id, false))
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
		t.Fatalf("turn kinds = %v", kindsOf(s.Turns))
	}
	if s.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2", s.Skipped)
	}
	if !slices.Equal(s.Todos, []session.Todo{
		{Content: "write tests", Status: "in_progress"},
		{Content: "ship", Status: "pending"},
	}) {
		t.Fatalf("todos = %+v", s.Todos)
	}
	if !slices.Equal(s.FilesTouched, []string{"internal/a.go", "internal/new.go", "internal/old.go"}) {
		t.Fatalf("files touched = %v", s.FilesTouched)
	}
	if st := a.DeriveStatus(s); st.Text != "turn completed" {
		t.Fatalf("status = %q", st.Text)
	}
}

// TestLoadSkipsDeveloperMessages covers the developer/system role skip.
func TestLoadSkipsDeveloperMessages(t *testing.T) {
	dir := t.TempDir()
	const ts = "2026-02-05T09:00:00Z"
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "02", "05",
		"rollout-2026-02-05T09-00-00-eeee0000-dev.jsonl"), []string{
		rolloutLine(ts, "session_meta", metaPayloadMap("/tmp/proj")),
		rolloutLine(ts, "response_item", map[string]any{"type": "message",
			"role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "dev instructions"}}}),
		rolloutLine(ts, "response_item", map[string]any{"type": "message",
			"role": "system", "content": []any{map[string]any{"type": "input_text", "text": "sys"}}}),
		rolloutLine(ts, "response_item", assistantMessage(textItem("output_text", "visible"))),
	})

	a := testAdapter(dir, nil)
	metas, err := a.List(t.Context(), listOpts(false, 0))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list dev fixture: %v %v", err, metas)
	}
	s, err := a.Load(t.Context(), metas[0])
	if err != nil {
		t.Fatal(err)
	}
	want := []session.TurnKind{session.AssistantText}
	if !slices.Equal(kindsOf(s.Turns), want) {
		t.Fatalf("turns = %v, want %v", kindsOf(s.Turns), want)
	}
}

// TestOutputObjectCompactJSON covers the object output shape without a
// content/output string field.
func TestOutputObjectCompactJSON(t *testing.T) {
	dir := t.TempDir()
	const ts = "2026-02-06T09:00:00Z"
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "02", "06",
		"rollout-2026-02-06T09-00-00-ffff0000-obj.jsonl"), []string{
		rolloutLine(ts, "session_meta", metaPayloadMap("/tmp/proj")),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call",
			"name": "shell", "call_id": "o1", "arguments": "{}"}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call_output",
			"call_id": "o1", "output": map[string]any{"stdout": "hi", "exit": 0}}),
	})

	a := testAdapter(dir, nil)
	metas, err := a.List(t.Context(), listOpts(false, 0))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list obj fixture: %v %v", err, metas)
	}
	s, err := a.Load(t.Context(), metas[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Turns[1].Text; got != `{"exit":0,"stdout":"hi"}` {
		t.Fatalf("compact object output = %q", got)
	}
}
