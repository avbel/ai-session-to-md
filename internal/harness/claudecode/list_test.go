package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

const fixtureRoot = "../../../testdata/claude"

func newFixtureAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(filepath.Join(fixtureRoot))
}

func setMtimes(t *testing.T, dir string, offsets map[string]time.Duration) {
	t.Helper()
	base := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for name, off := range offsets {
		path := filepath.Join(dir, "projects", "-tmp-proj", name+".jsonl")
		at := base.Add(off)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
}

func fixtureOffsets() map[string]time.Duration {
	return map[string]time.Duration{
		"plain":      5 * time.Hour,
		"rewind":     4 * time.Hour,
		"compact":    3 * time.Hour,
		"sidechain":  2 * time.Hour,
		"noisy":      1 * time.Hour,
		"tasks":      30 * time.Minute,
		"todo-reset": 0,
	}
}

func TestListOrder(t *testing.T) {
	a := newFixtureAdapter(t)
	setMtimes(t, fixtureRoot, fixtureOffsets())
	metas, err := a.List(context.Background(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"plain", "rewind", "compact", "sidechain", "noisy", "tasks", "todo-reset"}
	if len(metas) != len(want) {
		t.Fatalf("got %d metas, want %d: %+v", len(metas), len(want), metas)
	}
	for i, id := range want {
		if metas[i].ID != id {
			t.Errorf("meta[%d].ID = %q, want %q", i, metas[i].ID, id)
		}
	}
	m := metas[0]
	if m.Harness != session.HarnessClaudeCode {
		t.Errorf("Harness = %q", m.Harness)
	}
	if m.Archived {
		t.Error("Archived = true")
	}
	if m.Source == "" || filepath.Base(m.Source) != "plain.jsonl" {
		t.Errorf("Source = %q", m.Source)
	}
	if !m.UpdatedAt.Equal(time.Date(2026, 11, 13, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("UpdatedAt = %v", m.UpdatedAt)
	}
}

func TestListLimit(t *testing.T) {
	a := newFixtureAdapter(t)
	setMtimes(t, fixtureRoot, fixtureOffsets())
	metas, err := a.List(context.Background(), harness.ListOptions{
		Scope: harness.Scope{All: true},
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 2 || metas[0].ID != "plain" || metas[1].ID != "rewind" {
		t.Fatalf("got %+v, want plain+rewind", metas)
	}
}

func TestListScope(t *testing.T) {
	a := newFixtureAdapter(t)
	setMtimes(t, fixtureRoot, fixtureOffsets())
	cases := []struct {
		name  string
		scope harness.Scope
		want  int
	}{
		{"root equal", harness.Scope{Root: "/tmp/proj"}, 7},
		{"root mismatch", harness.Scope{Root: "/tmp/other"}, 0},
		{"no root not all", harness.Scope{}, 0},
	}
	for _, tc := range cases {
		metas, err := a.List(context.Background(), harness.ListOptions{Scope: tc.scope})
		if err != nil {
			t.Fatalf("%s: List: %v", tc.name, err)
		}
		if len(metas) != tc.want {
			t.Errorf("%s: got %d sessions, want %d", tc.name, len(metas), tc.want)
		}
	}
}

func TestListMetadata(t *testing.T) {
	a := newFixtureAdapter(t)
	metas, err := a.List(context.Background(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]session.Meta{}
	for _, m := range metas {
		byID[m.ID] = m
	}
	p := byID["plain"]
	if p.Title != "Fix the failing test in internal/reduce" {
		t.Errorf("plain title = %q", p.Title)
	}
	if p.Cwd != "/tmp/proj" {
		t.Errorf("plain cwd = %q, want first cwd", p.Cwd)
	}
	if p.Branch != "feature" {
		t.Errorf("plain branch = %q, want last gitBranch", p.Branch)
	}
	n := byID["noisy"]
	if n.Title != "Custom title" {
		t.Errorf("noisy title = %q, want custom title", n.Title)
	}
}

func TestListTitlePrecedence(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-tmp-titles")
	mkdir(t, proj)
	files := map[string]string{
		"t-user":         userLine("u1", `"plain title fallback"`),
		"t-ai":           `{"type":"user","uuid":"u1","cwd":"/tmp/t","message":{"role":"user","content":"first"}}` + "\n" + `{"type":"ai-title","uuid":"t1","aiTitle":"AI title"}`,
		"t-custom":       `{"type":"user","uuid":"u1","cwd":"/tmp/t","message":{"role":"user","content":"first"}}` + "\n" + `{"type":"ai-title","uuid":"t1","aiTitle":"AI title"}` + "\n" + `{"type":"custom-title","uuid":"t2","customTitle":"Custom title"}`,
		"t-prompt":       `{"type":"user","uuid":"u1","cwd":"/tmp/t","message":{"role":"user","content":"first"}}` + "\n" + `{"type":"last-prompt","uuid":"t3","lastPrompt":"Last prompt"}`,
		"t-empty-prompt": `{"type":"user","uuid":"u1","cwd":"/tmp/t","message":{"role":"user","content":"first"}}` + "\n" + `{"type":"last-prompt","uuid":"t4","leafUuid":"u1"}`,
	}
	want := map[string]string{
		"t-user":         "plain title fallback",
		"t-ai":           "AI title",
		"t-custom":       "Custom title",
		"t-prompt":       "Last prompt",
		"t-empty-prompt": "first",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(content+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	a := New(dir)
	metas, err := a.List(context.Background(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != len(want) {
		t.Fatalf("got %d metas, want %d", len(metas), len(want))
	}
	for _, m := range metas {
		if m.Title != want[m.ID] {
			t.Errorf("%s title = %q, want %q", m.ID, m.Title, want[m.ID])
		}
	}
}

func TestListSkipsEmptyAndNested(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-tmp-empty")
	mkdir(t, proj)
	write(t, filepath.Join(proj, "empty.jsonl"),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"x"}]}}`)
	write(t, filepath.Join(proj, "good.jsonl"), userLine("u1", `"hello"`))
	mkdir(t, filepath.Join(proj, "good", "subagents"))
	write(t, filepath.Join(proj, "good", "subagents", "sub.jsonl"), userLine("s1", `"subagent"`))
	mkdir(t, filepath.Join(proj, "nested"))
	write(t, filepath.Join(proj, "nested", "inner.jsonl"), userLine("n1", `"nested"`))

	a := New(dir)
	metas, err := a.List(context.Background(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "good" {
		t.Fatalf("got %+v, want only good", metas)
	}
}

func userLine(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","cwd":"/tmp/proj","message":{"role":"user","content":` + content + `}}`
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestListMissingDir(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), "nope"))
	metas, err := a.List(context.Background(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("got %d metas, want 0", len(metas))
	}
}

func TestListContextCancelled(t *testing.T) {
	a := newFixtureAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.List(ctx, harness.ListOptions{Scope: harness.Scope{All: true}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
