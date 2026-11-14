//go:build e2e

package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

const codexEpochMs = int64(1_763_000_000_000)

// codexThreadRow is one row of the synthetic threads table.
type codexThreadRow struct {
	id, rollout, cwd, title, name, origin, threadSource string
	updatedMs                                           int64
}

// writeCodexStore builds state_5.sqlite plus the rollouts it references. The
// subagent and spawned-child rows are newer than the main row on purpose: if
// the filters regressed, --latest would pick them instead.
func writeCodexStore(t *testing.T, e *testEnv) {
	t.Helper()
	dir := e.codexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	mainPath := writeCodexRollout(t, dir, "02", codexSessionID, mainCodexLines(e))
	subPath := writeCodexRollout(t, dir, "03", codexSubID, otherCodexLines(e.repo, codexSubID, "SUBAGENT ONLY", "exec", "subagent", fakeRemoteURL))
	childPath := writeCodexRollout(t, dir, "04", codexChildID, otherCodexLines(e.repo, codexChildID, "CHILD ONLY", "exec", "user", fakeRemoteURL))
	otherPath := writeCodexRollout(t, dir, "05", codexOtherID, otherCodexLines("/elsewhere", codexOtherID, "OTHER PROJECT", "exec", "user", "https://github.com/example/other.git"))
	writeFile(t, filepath.Join(dir, "session_index.jsonl"),
		`{"id":"`+codexSessionID+`","thread_name":"Indexed Codex Title","updated_at":100}`+"\n")

	rows := []codexThreadRow{
		{codexSessionID, mainPath, e.repo, "DB title first line\nignored second line", "Codex E2E session", fakeRemoteURL, "user", codexEpochMs},
		{codexSubID, subPath, e.repo, "", "", fakeRemoteURL, "subagent", codexEpochMs + 3000},
		{codexChildID, childPath, e.repo, "", "", fakeRemoteURL, "user", codexEpochMs + 4000},
		{codexOtherID, otherPath, "/elsewhere", "", "", "https://github.com/example/other.git", "user", codexEpochMs + 2000},
	}
	writeCodexDB(t, filepath.Join(dir, "state_5.sqlite"), rows)
}

// writeCodexDB creates the threads schema and inserts the rows.
func writeCodexDB(t *testing.T, path string, rows []codexThreadRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open codex fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	schema := `CREATE TABLE threads (
		id TEXT, rollout_path TEXT, created_at INTEGER, cwd TEXT, title TEXT,
		name TEXT, updated_at INTEGER, updated_at_ms INTEGER, archived INTEGER,
		git_branch TEXT, git_origin_url TEXT, thread_source TEXT, source TEXT);
	CREATE TABLE thread_spawn_edges (
		parent_thread_id TEXT, child_thread_id TEXT);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create codex schema: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO threads (id, rollout_path, created_at, cwd,
			title, name, updated_at, updated_at_ms, archived, git_branch,
			git_origin_url, thread_source, source) VALUES (?,?,?,?,?,?,?,?,0,'main',?,?,'exec')`,
			r.id, r.rollout, r.updatedMs/1000, r.cwd, r.title, r.name,
			r.updatedMs/1000, r.updatedMs, r.origin, r.threadSource); err != nil {
			t.Fatalf("insert codex thread %s: %v", r.id, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id)
		VALUES (?, ?)`, codexSessionID, codexChildID); err != nil {
		t.Fatalf("insert codex spawn edge: %v", err)
	}
}

// writeCodexRollout writes one rollout file and returns its absolute path.
func writeCodexRollout(t *testing.T, dir, day, id string, lines []string) string {
	t.Helper()
	name := fmt.Sprintf("rollout-2026-01-%sT10-00-00-%s.jsonl", day, id)
	path := filepath.Join(dir, "sessions", "2026", "01", day, name)
	var data []byte
	for _, line := range lines {
		data = append(data, line...)
		data = append(data, '\n')
	}
	writeFile(t, path, string(data))
	return path
}

// codexLine marshals one {timestamp, type, payload} rollout entry.
func codexLine(typ string, payload any) string {
	raw, err := json.Marshal(map[string]any{
		"timestamp": "2026-01-02T10:00:00Z", "type": typ, "payload": payload,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func codexSessionMeta(cwd, id, source, threadSource, origin string) map[string]any {
	return map[string]any{
		"session_id": id, "id": id, "cwd": cwd, "cli_version": codexVersion,
		"source": source, "thread_source": threadSource,
		"git": map[string]any{
			"repository_url": origin, "branch": "main", "commit_hash": "abc123def456",
		},
	}
}

func codexUserMessage(text string) map[string]any {
	return map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
}

func codexAssistantMessage(text string) map[string]any {
	return map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	}
}

// mainCodexLines ends with assistant text so the status is "turn completed"
// and the update_plan call is an orphan before the last user message.
func mainCodexLines(e *testEnv) []string {
	return []string{
		codexLine("session_meta", codexSessionMeta(e.repo, codexSessionID, "exec", "user", fakeRemoteURL)),
		codexLine("turn_context", map[string]any{"model": codexModel, "cwd": e.repo}),
		codexLine("event_msg", map[string]any{"type": "task_started"}),
		codexLine("response_item", codexUserMessage("Please fix the codex bug")),
		codexLine("response_item", codexAssistantMessage("Looking at it.")),
		codexLine("response_item", map[string]any{
			"type": "function_call", "name": "update_plan", "call_id": "call_plan",
			"arguments": `{"plan":[{"step":"write the codex fix","status":"completed"},{"step":"run codex tests","status":"in_progress"}]}`,
		}),
		codexLine("response_item", codexUserMessage("now run the tests")),
		codexLine("response_item", codexAssistantMessage("Tests pass now.")),
		codexLine("event_msg", map[string]any{"type": "task_complete"}),
	}
}

// otherCodexLines is the minimal stream used by the rows the filters must
// drop.
func otherCodexLines(cwd, id, text, source, threadSource, origin string) []string {
	return []string{
		codexLine("session_meta", codexSessionMeta(cwd, id, source, threadSource, origin)),
		codexLine("turn_context", map[string]any{"model": codexModel, "cwd": cwd}),
		codexLine("response_item", codexUserMessage(text)),
		codexLine("response_item", codexAssistantMessage("ok: "+text)),
	}
}

// writeOpenCodeStore builds the v2 database: one main session, one spawned
// child and one session from another project.
func writeOpenCodeStore(t *testing.T, e *testEnv) {
	t.Helper()
	dir := e.openCodeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatalf("open opencode fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	createOpenCodeSchema(t, db)
	insertOpenCodeData(t, db, e)
}

func createOpenCodeSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, ddl := range []string{
		`PRAGMA journal_mode=DELETE`,
		`CREATE TABLE session_v2 (
			id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, title TEXT,
			version TEXT, model TEXT, time_created INTEGER, time_updated INTEGER,
			time_archived INTEGER)`,
		`CREATE TABLE session_message (
			session_id TEXT, id TEXT, seq INTEGER, type TEXT, data TEXT,
			time_created INTEGER)`,
		`CREATE TABLE todo (
			session_id TEXT, position INTEGER, content TEXT, status TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("exec %q: %v", ddl, err)
		}
	}
}

func insertOpenCodeData(t *testing.T, db *sql.DB, e *testEnv) {
	t.Helper()
	base := codexEpochMs
	insertOpenCodeSession(t, db, openCodeID, "", e.repo, "OpenCode E2E session", base)
	insertOpenCodeSession(t, db, openCodeChildID, openCodeID, e.repo, "child", base+3000)
	insertOpenCodeSession(t, db, openCodeOtherID, "", "/elsewhere", "other", base+4000)

	insertOpenCodeMessage(t, db, openCodeID, 1, "user", map[string]any{"text": "Please write the opencode adapter"})
	insertOpenCodeMessage(t, db, openCodeID, 2, "assistant", map[string]any{
		"model": "openai/" + codexModel, "finish": "stop",
		"content": []any{
			map[string]any{"type": "text", "text": "Writing the adapter now."},
			map[string]any{
				"type": "tool", "id": "part_1", "name": "read",
				"state": map[string]any{
					"status": "completed", "input": map[string]any{"path": "internal/opencode.go"},
					"content": "package opencode",
				},
			},
		},
	})
	insertOpenCodeMessage(t, db, openCodeID, 3, "synthetic", map[string]any{"text": "SYNTHETIC ONLY reminder"})
	insertOpenCodeMessage(t, db, openCodeChildID, 1, "user", map[string]any{"text": "CHILD ONLY"})
	for i, todo := range []struct{ content, status string }{
		{"write the opencode adapter", "completed"},
		{"run the opencode tests", "in_progress"},
	} {
		if _, err := db.Exec(`INSERT INTO todo (session_id, position, content, status)
			VALUES (?,?,?,?)`, openCodeID, i+1, todo.content, todo.status); err != nil {
			t.Fatalf("insert opencode todo: %v", err)
		}
	}
}

func insertOpenCodeSession(t *testing.T, db *sql.DB, id, parent, dir, title string, updated int64) {
	t.Helper()
	model := `{"id":"` + codexModel + `","providerID":"openai"}`
	if _, err := db.Exec(`INSERT INTO session_v2 (id, parent_id, directory, title, version,
		model, time_created, time_updated, time_archived) VALUES (?,?,?,?,?,?,?,?,NULL)`,
		id, nullable(parent), dir, title, "1.2.3", model, updated-1000, updated); err != nil {
		t.Fatalf("insert opencode session %s: %v", id, err)
	}
}

func insertOpenCodeMessage(t *testing.T, db *sql.DB, session string, seq int, typ string, data any) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal opencode message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (session_id, id, seq, type, data, time_created)
		VALUES (?,?,?,?,?,?)`, session, fmt.Sprintf("m%d", seq), seq, typ, string(raw), int64(seq)); err != nil {
		t.Fatalf("insert opencode message: %v", err)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
