package opencode

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// buildTestDB creates the v2 schema in a fresh temp file and returns its
// path. All SQL is written here from Go; no database binary is committed.
func buildTestDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range v2Schema() {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("exec %q: %v", ddl, err)
		}
	}
	return path, db
}

func v2Schema() []string {
	return []string{
		`CREATE TABLE session_v2 (
			id TEXT PRIMARY KEY,
			parent_id TEXT,
			directory TEXT,
			title TEXT,
			version TEXT,
			model TEXT,
			time_created INTEGER,
			time_updated INTEGER,
			time_archived INTEGER
		)`,
		`CREATE TABLE session_message (
			session_id TEXT,
			id TEXT,
			seq INTEGER,
			type TEXT,
			data TEXT,
			time_created INTEGER
		)`,
		`CREATE TABLE todo (
			session_id TEXT,
			position INTEGER,
			content TEXT,
			status TEXT
		)`,
	}
}

func execQ(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func insertSession(t *testing.T, db *sql.DB, id, dir, title, model, version string, updated int64, archived *int64) {
	t.Helper()
	execQ(t, db,
		`INSERT INTO session_v2 (id, directory, title, version, model, time_created, time_updated, time_archived)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, dir, title, version, model, updated-1000, updated, archived)
}

func insertMsg(t *testing.T, db *sql.DB, sess string, seq int64, typ string, data any, created int64) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal message data: %v", err)
	}
	insertRawMsg(t, db, sess, seq, typ, string(raw))
	execQ(t, db, `UPDATE session_message SET time_created=? WHERE session_id=? AND seq=?`, created, sess, seq)
}

// insertRawMsg inserts a message whose data need not be valid JSON.
func insertRawMsg(t *testing.T, db *sql.DB, sess string, seq int64, typ, data string) {
	t.Helper()
	execQ(t, db,
		`INSERT INTO session_message (session_id, id, seq, type, data, time_created)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess, "m"+string(rune('0'+seq)), seq, typ, data, int64(seq))
}

func toolItem(id, name, status string, input, content any) map[string]any {
	return map[string]any{
		"type": "tool", "id": id, "name": name,
		"state": map[string]any{"status": status, "input": input, "content": content},
	}
}

// listFixtureDB seeds three top-level sessions (one archived, one child, one
// with an empty title) for the List tests.
func listFixtureDB(t *testing.T) (string, *Adapter) {
	t.Helper()
	path, db := buildTestDB(t)
	var archived int64 = 1763000040000
	insertSession(t, db, "ses_proj01", "/proj/sub", "t1", "", "v1", 1763000010000, nil)
	insertSession(t, db, "ses_other1", "/elsewhere", "", "", "v1", 1763000020000, nil)
	insertSession(t, db, "ses_oldarc1", "/proj/deep", "old", "", "v1", 1763000030000, &archived)
	insertSession(t, db, "ses_child01", "/proj/sub", "child", "", "v1", 1763000050000, nil)
	execQ(t, db, `UPDATE session_v2 SET parent_id=? WHERE id=?`, "ses_proj01", "ses_child01")
	insertMsg(t, db, "ses_other1", 1, "user", map[string]any{"text": "first user message text here"}, 1)
	return path, New(filepath.Dir(path))
}

func idsOf(metas []session.Meta) []string {
	out := make([]string, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.ID)
	}
	return out
}

func TestListV2Scope(t *testing.T) {
	_, a := listFixtureDB(t)
	metas, err := a.List(t.Context(), listOpts(withScopeRoot("/proj")))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := idsOf(metas); len(got) != 1 || got[0] != "ses_proj01" {
		t.Fatalf("scope /proj: got %v, want [ses_proj01] (child and archived excluded)", got)
	}
}

func TestListV2OrderAndArchived(t *testing.T) {
	_, a := listFixtureDB(t)
	metas, err := a.List(t.Context(), listOptAll())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"ses_other1", "ses_proj01"} // newest first, child excluded
	if got := idsOf(metas); !equal(got, want) {
		t.Fatalf("all: got %v, want %v", got, want)
	}
	metas, err = a.List(t.Context(), listOpts(withScopeAll(), withArchived()))
	if err != nil {
		t.Fatalf("List archived: %v", err)
	}
	if got, want := idsOf(metas), 3; len(got) != want {
		t.Fatalf("all+archived: got %d rows, want %d", len(got), want)
	}
}

func TestListV2Limit(t *testing.T) {
	_, a := listFixtureDB(t)
	metas, err := a.List(t.Context(), listOpts(withScopeAll(), withLimit(1)))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := idsOf(metas); len(got) != 1 || got[0] != "ses_other1" {
		t.Fatalf("limit 1: got %v, want [ses_other1]", got)
	}
}

func TestListV2Meta(t *testing.T) {
	path, a := listFixtureDB(t)
	metas, err := a.List(t.Context(), listOpts(withScopeAll(), withArchived()))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]session.Meta{}
	for _, m := range metas {
		byID[m.ID] = m
	}
	proj := byID["ses_proj01"]
	if proj.Source != "db:"+filepath.Join(filepath.Dir(path), "opencode.db")+"#ses_proj01" {
		t.Fatalf("Source = %q", proj.Source)
	}
	if proj.UpdatedAt.UnixMilli() != 1763000010000 {
		t.Fatalf("UpdatedAt = %v", proj.UpdatedAt)
	}
	if proj.Harness != session.HarnessOpenCode {
		t.Fatalf("Harness = %q", proj.Harness)
	}
	if proj.Title != "t1" {
		t.Fatalf("Title = %q", proj.Title)
	}
	// Empty title falls back to the first user message text.
	if got := byID["ses_other1"].Title; got != "first user message text here" {
		t.Fatalf("title fallback = %q", got)
	}
	if !byID["ses_oldarc1"].Archived {
		t.Fatal("archived flag not set from time_archived")
	}
}

// TestBuilderDropsEmptyUserRowsSilently reproduces the real-data finding:
// 119 of 915 real user rows are {text:"", time}; they are empty, not
// unparseable, and must not inflate the "skipped N unparseable lines" count.
func TestBuilderDropsEmptyUserRowsSilently(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_emptyrow", "/proj", "t", `{"id":"gpt-5","providerID":"openai"}`, "0.4.0", 1763000100000, nil)
	insertMsg(t, db, "ses_emptyrow", 1, "user", map[string]any{"text": ""}, 100)
	insertMsg(t, db, "ses_emptyrow", 2, "user", map[string]any{"text": "   "}, 200)
	insertRawMsg(t, db, "ses_emptyrow", 3, "user", "{not json")
	insertMsg(t, db, "ses_emptyrow", 4, "user", map[string]any{"text": "real"}, 300)
	_ = db.Close()

	a := New(filepath.Dir(path))
	s, _ := mustLoad(t, a, "ses_emptyrow")
	if s.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1 (only the malformed row counts)", s.Skipped)
	}
	if got := kindsString(s); got != "user" {
		t.Fatalf("kinds = %q, want a single user turn", got)
	}
	if len(s.Turns) != 1 || s.Turns[0].Text != "real" {
		t.Fatalf("turns = %+v", s.Turns)
	}
}

// fixtureDB seeds the standard test session: a user message with files, an
// assistant turn with text, reasoning and a completed bash tool, a
// compaction, a synthetic message, and a final assistant whose content is
// produced by variant. Todos come from the todo table.
func fixtureDB(t *testing.T, variant func() map[string]any) (string, *Adapter) {
	t.Helper()
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_fix01", "/proj", "fixture", `{"id":"gpt-5","providerID":"openai"}`, "0.4.0", 1763000100000, nil)
	insertMsg(t, db, "ses_fix01", 1, "user",
		map[string]any{"text": "fix the bug", "files": []string{"a.go", "b.ts"}}, 100)
	insertMsg(t, db, "ses_fix01", 2, "assistant", map[string]any{
		"model": "anthropic/claude-x",
		"content": []any{
			map[string]any{"type": "text", "text": "on it"},
			map[string]any{"type": "reasoning", "text": "think"},
			toolItem("t1", "bash", "completed", map[string]any{"command": "ls"}, []any{map[string]any{"text": "file list"}}),
		},
	}, 200)
	insertMsg(t, db, "ses_fix01", 3, "compaction", map[string]any{"summary": "earlier"}, 300)
	insertMsg(t, db, "ses_fix01", 4, "synthetic", map[string]any{"text": "fake sk-aaaaaaaaaaaaaaaaaaaaaaaa"}, 400)
	insertMsg(t, db, "ses_fix01", 5, "assistant", variant(), 500)
	execQ(t, db, `INSERT INTO todo (session_id, position, content, status) VALUES (?, ?, ?, ?)`,
		"ses_fix01", 1, "step one", "completed")
	execQ(t, db, `INSERT INTO todo (session_id, position, content, status) VALUES (?, ?, ?, ?)`,
		"ses_fix01", 2, "step two", "in_progress")
	return path, New(filepath.Dir(path))
}

func mustLoad(t *testing.T, a *Adapter, id string) (session.Session, session.Info) {
	t.Helper()
	res, err := a.LoadDetailed(t.Context(), session.Meta{ID: id})
	if err != nil {
		t.Fatalf("LoadDetailed(%s): %v", id, err)
	}
	return res.Session, res.Info
}

func kindsString(s session.Session) string {
	parts := make([]string, 0, len(s.Turns))
	for _, turn := range s.Turns {
		parts = append(parts, turn.Kind.String())
	}
	return strings.Join(parts, ",")
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadV2Turns(t *testing.T) {
	_, a := fixtureDB(t, func() map[string]any {
		return map[string]any{
			"content": []any{toolItem("t2", "write", "pending", map[string]any{"filePath": "/proj/x.go"}, nil)},
		}
	})
	s, _ := mustLoad(t, a, "ses_fix01")

	want := "user,assistant,reasoning,tool_call,tool_result,compaction_summary,tool_call"
	if got := kindsString(s); got != want {
		t.Fatalf("kinds = %q, want %q (synthetic skipped)", got, want)
	}
	if s.Turns[0].Text != "fix the bug [file: a.go] [file: b.ts]" {
		t.Fatalf("user text = %q", s.Turns[0].Text)
	}
	if s.Turns[4].Text != "file list" || s.Turns[4].ToolID != "t1" {
		t.Fatalf("tool result = %+v", s.Turns[4])
	}
	if s.Turns[6].ToolName != "write" || s.Turns[6].Input != `{"filePath":"/proj/x.go"}` {
		t.Fatalf("pending tool call = %+v", s.Turns[6])
	}
	for _, turn := range s.Turns {
		if strings.Contains(turn.Text, "sk-aaa") {
			t.Fatalf("synthetic text leaked: %q", turn.Text)
		}
	}
	if s.Skipped != 0 {
		t.Fatalf("Skipped = %d", s.Skipped)
	}
	if len(s.FilesTouched) != 1 || s.FilesTouched[0] != "/proj/x.go" {
		t.Fatalf("files = %v", s.FilesTouched)
	}
}

func TestLoadV2Meta(t *testing.T) {
	path, a := fixtureDB(t, func() map[string]any {
		return map[string]any{"content": []any{}}
	})
	s, info := mustLoad(t, a, "ses_fix01")
	if s.Model != "anthropic/claude-x" {
		t.Fatalf("model = %q, want assistant override", s.Model)
	}
	if s.HarnessVer != "0.4.0" {
		t.Fatalf("HarnessVer = %q", s.HarnessVer)
	}
	if s.Cwd != "/proj" || s.Title != "fixture" {
		t.Fatalf("meta = %+v", s.Meta)
	}
	if s.Source != "db:"+path+"#ses_fix01" {
		t.Fatalf("Source = %q", s.Source)
	}
	if len(s.Todos) != 2 || s.Todos[1].Status != "in_progress" {
		t.Fatalf("todos = %+v", s.Todos)
	}
	if !info.HasFinish || info.Finish != "" {
		t.Fatalf("info = %+v, want HasFinish with empty finish", info)
	}
	if info.MidTurn {
		t.Fatal("MidTurn = true, want false")
	}
}
