package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// codexFixtureDir is the synthetic $CODEX_HOME built once in TestMain. It
// is never committed and holds no real data.
var codexFixtureDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aismd-codex-fixture")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktemp:", err)
		os.Exit(1)
	}
	codexFixtureDir = dir
	if err := buildDBFixture(dir); err != nil {
		fmt.Fprintln(os.Stderr, "build codex fixture:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const (
	idFull  = "11111111-full"
	idSub   = "22222222-subagent"
	idNoJS  = "33333333-nojs"
	idEmpty = "44444444-empty"
	idArch  = "55555555-archived"
	idChild = "66666666-child"
	idMiss  = "77777777-missing"
	idRert  = "88888888-reroot"
	idTitl  = "99999999-title"
	idGlob  = "10101010-glob"
)

// buildDBFixture creates the state database and its rollout files.
func buildDBFixture(dir string) error {
	for _, d := range [][]string{
		{"sessions", "2026", "01", "02"},
		{"sessions", "2026", "01", "03"},
		{"sessions", "2026", "01", "04"},
		{"sessions", "2026", "01", "05"},
		{"sessions", "2026", "01", "06"},
		{"sessions", "2026", "01", "07"},
		{"sessions", "2026", "01", "08"},
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Join(d...)), 0o755); err != nil {
			return err
		}
	}

	fullPath := writeFixtureRollout(dir, "2026", "01", "02", idFull, fullRolloutLines())
	writeFixtureRollout(dir, "2026", "01", "03", idRert, minimalRolloutLines("/tmp/proj", "rerooted"))
	writeFixtureRollout(dir, "2026", "01", "04", idGlob, minimalRolloutLines("/tmp/proj", "globby"))
	writeFixtureRollout(dir, "2026", "01", "05", idArch, minimalRolloutLines("/tmp/proj", "archived"))
	writeFixtureRollout(dir, "2026", "01", "06", idChild, minimalRolloutLines("/tmp/proj", "child"))
	writeFixtureRollout(dir, "2026", "01", "07", idTitl, minimalRolloutLines("/tmp/proj", "titled"))
	emptyPath := writeFixtureRollout(dir, "2026", "01", "08", idEmpty, minimalRolloutLines("/tmp/proj", "empty"))

	if err := writeSessionIndex(dir); err != nil {
		return err
	}
	return insertThreadRows(dir, fullPath, emptyPath)
}

// writeFixtureRollout writes a rollout file named after its thread id and
// returns its absolute path.
func writeFixtureRollout(dir, yyyy, mm, dd, id string, lines []string) string {
	name := fmt.Sprintf("rollout-2026-%s-%sT10-00-00-%s.jsonl", mm, dd, id)
	path := filepath.Join(dir, "sessions", yyyy, mm, dd, name)
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o644); err != nil {
		panic(err)
	}
	return path
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

// writeSessionIndex maps idRert to a thread_name and includes a malformed
// line the reader must tolerate.
func writeSessionIndex(dir string) error {
	content := `{"id":"` + idRert + `","thread_name":"Indexed Title","updated_at":100}
{oops
`
	return os.WriteFile(filepath.Join(dir, "session_index.jsonl"), []byte(content), 0o644)
}

type threadInsert struct {
	id           string
	rolloutPath  string
	cwd          string
	title        string
	name         string
	source       string
	threadSource string
	archived     int
	updatedMs    int64
	origin       string
}

func insertThreadRows(dir, fullPath, emptyPath string) error {
	rows := []threadInsert{
		{id: idFull, rolloutPath: fullPath, cwd: "/tmp/proj", name: "Full Thread",
			source: "exec", threadSource: "user", updatedMs: 5000,
			origin: "https://github.com/example/one.git"},
		{id: idSub, cwd: "/tmp/proj", source: "exec", threadSource: "subagent", updatedMs: 4900},
		{id: idNoJS, cwd: "/tmp/proj", source: `{"subagent":{"task":"x"}}`, threadSource: "", updatedMs: 4800},
		{id: idEmpty, rolloutPath: emptyPath, cwd: "/tmp/proj", title: "Empty Thread", source: "exec", threadSource: "", updatedMs: 4000},
		{id: idArch, rolloutPath: fixtureRolloutPath(dir, "05", idArch), cwd: "/tmp/proj",
			title: "Archived Thread", source: "exec", threadSource: "user", archived: 1, updatedMs: 6000},
		{id: idChild, rolloutPath: fixtureRolloutPath(dir, "06", idChild), cwd: "/tmp/proj",
			source: "exec", threadSource: "user", updatedMs: 7000},
		{id: idMiss, rolloutPath: "/nonexistent/rollout-x.jsonl", cwd: "/tmp/proj",
			source: "exec", threadSource: "user", updatedMs: 8000},
		{id: idRert, rolloutPath: "/old/home/.codex/sessions/2026/01/03/rollout-2026-01-03T10-00-00-" + idRert + ".jsonl",
			cwd: "/tmp/proj", title: "DB Title", source: "exec", threadSource: "user", updatedMs: 3000},
		{id: idTitl, rolloutPath: fixtureRolloutPath(dir, "07", idTitl), cwd: "/tmp/proj",
			title: "First line title\nsecond line", source: "exec", threadSource: "user", updatedMs: 2000},
		{id: idGlob, cwd: "/elsewhere", title: "Glob Title", source: "exec", threadSource: "user", updatedMs: 1000,
			origin: "ssh://git@github.com/example/two.git"},
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "state_5.sqlite"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	schema := `CREATE TABLE threads (
		id TEXT, rollout_path TEXT, created_at INTEGER, cwd TEXT, title TEXT,
		name TEXT, updated_at INTEGER, updated_at_ms INTEGER, archived INTEGER,
		git_branch TEXT, git_origin_url TEXT, thread_source TEXT, source TEXT);
	CREATE TABLE thread_spawn_edges (
		parent_thread_id TEXT, child_thread_id TEXT);`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO threads (id, rollout_path, created_at, cwd,
			title, name, updated_at, updated_at_ms, archived, git_branch,
			git_origin_url, thread_source, source) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.id, r.rolloutPath, r.updatedMs/1000, r.cwd, r.title, r.name,
			r.updatedMs/1000, r.updatedMs, r.archived, "main", r.origin,
			r.threadSource, r.source); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id)
		VALUES (?, ?)`, idFull, idChild); err != nil {
		return err
	}
	return nil
}

func fixtureRolloutPath(dir, dd, id string) string {
	name := fmt.Sprintf("rollout-2026-01-%sT10-00-00-%s.jsonl", dd, id)
	return filepath.Join(dir, "sessions", "2026", "01", dd, name)
}

// rolloutLine marshals one {timestamp, type, payload} rollout line.
func rolloutLine(ts, typ string, payload any) string {
	b, err := json.Marshal(map[string]any{"timestamp": ts, "type": typ, "payload": payload})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func textItem(typ, text string) map[string]any {
	return map[string]any{"type": typ, "text": text}
}

func metaPayloadMap(cwd string) map[string]any {
	return map[string]any{
		"session_id":    "sid",
		"id":            "metaid",
		"cwd":           cwd,
		"cli_version":   "0.42.0",
		"source":        "exec",
		"thread_source": "user",
		"git": map[string]any{
			"repository_url": "https://github.com/example/one.git",
			"branch":         "main",
			"commit_hash":    "abc123def",
		},
	}
}

func userMessage(items ...map[string]any) map[string]any {
	return map[string]any{"type": "message", "role": "user", "content": items}
}

func assistantMessage(items ...map[string]any) map[string]any {
	return map[string]any{"type": "message", "role": "assistant", "content": items}
}

// fullRolloutLines covers every §7.2 response_item rule in one stream.
func fullRolloutLines() []string {
	const ts = "2026-01-02T10:00:00Z"
	return []string{
		rolloutLine(ts, "session_meta", metaPayloadMap("/tmp/proj")),
		rolloutLine(ts, "turn_context", map[string]any{"model": "gpt-5.2", "cwd": "/tmp/proj"}),
		rolloutLine(ts, "response_item", userMessage(
			textItem("input_text", "# AGENTS.md instructions\nobey"),
			textItem("input_text", "<environment_context>\ncwd=/tmp/proj"),
			textItem("input_text", "<guardian_tool_descriptions>\nhidden"),
			textItem("input_text", "Please fix the bug"),
		)),
		rolloutLine(ts, "response_item", map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_image"}}}),
		rolloutLine(ts, "response_item", assistantMessage(textItem("output_text", "I will fix it now."))),
		rolloutLine(ts, "response_item", map[string]any{"type": "agent_message",
			"recipient": "reviewer", "content": []any{map[string]any{"text": "review please"}}}),
		rolloutLine(ts, "response_item", map[string]any{"type": "reasoning",
			"summary": []any{map[string]any{"text": "thinking hard"}, map[string]any{"text": "more"}}}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call",
			"name": "update_plan", "call_id": "callPlan",
			"arguments": `{"plan":[{"step":"write tests","status":"in_progress"},{"step":"ship","status":"pending"}]}`}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call",
			"name": "apply_patch", "call_id": "callA",
			"arguments": `{"input":"*** Begin Patch\n*** Update File: internal/a.go\n*** Add File: internal/new.go\n*** Delete File: internal/old.go\n","summary":"patch"}`}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call_output",
			"call_id": "callA", "output": "patch applied"}),
		rolloutLine(ts, "response_item", map[string]any{"type": "custom_tool_call",
			"name": "shell", "call_id": "callB", "input": "ls -la"}),
		rolloutLine(ts, "response_item", map[string]any{"type": "custom_tool_call_output",
			"call_id": "callB", "output": []any{
				map[string]any{"type": "input_text", "text": "file1"},
				map[string]any{"type": "input_text", "text": "file2"}}}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call",
			"name": "run", "call_id": "callC", "arguments": `{"cmd":"go test"}`}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call_output",
			"call_id": "callC", "output": map[string]any{"content": "all passed"}}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call",
			"name": "probe", "call_id": nil, "arguments": `{"q":1}`}),
		rolloutLine(ts, "response_item", map[string]any{"type": "function_call_output",
			"call_id": nil, "output": map[string]any{"output": "probe result"}}),
		rolloutLine(ts, "compacted", map[string]any{"message": "earlier context summarized"}),
		rolloutLine(ts, "event_msg", map[string]any{"type": "task_started"}),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", "continue please"))),
		rolloutLine(ts, "response_item", assistantMessage(textItem("output_text", "Done. All tests pass."))),
		rolloutLine(ts, "event_msg", map[string]any{"type": "task_complete"}),
		"{not json",
		rolloutLine(ts, "world_state", map[string]any{"full": true}),
	}
}

// minimalRolloutLines is the smallest complete session stream.
func minimalRolloutLines(cwd, text string) []string {
	const ts = "2026-01-02T10:00:00Z"
	return []string{
		rolloutLine(ts, "session_meta", metaPayloadMap(cwd)),
		rolloutLine(ts, "turn_context", map[string]any{"model": "gpt-5.2", "cwd": cwd}),
		rolloutLine(ts, "event_msg", map[string]any{"type": "task_started"}),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", text))),
		rolloutLine(ts, "response_item", assistantMessage(textItem("output_text", "ok: "+text))),
		rolloutLine(ts, "event_msg", map[string]any{"type": "task_complete"}),
	}
}

// writeFallbackRollouts builds a DB-less fixture dir with four sessions:
// ids[0] plain user, ids[1] subagent (dropped), ids[2] plain user with
// empty thread_source, ids[3] archived .zst rollout. Mtimes are set so the
// UpdatedAt-desc order is deterministic: ids[2], ids[3], ids[0].
func writeFallbackRollouts(t *testing.T, dir string, ids ...string) {
	t.Helper()
	f1, f2, f3, f4 := ids[0], ids[1], ids[2], ids[3]

	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "03", "04",
		"rollout-2026-03-04T09-00-00-"+f1+".jsonl"),
		fallbackLines(f1, "main", "exec", "user", "file only"))
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "03", "05",
		"rollout-2026-03-05T09-00-00-"+f2+".jsonl"),
		fallbackLines(f2, "", map[string]any{"subagent": map[string]any{"task": "x"}}, "", "sub work"))
	writeRolloutFile(t, filepath.Join(dir, "sessions", "2026", "03", "06",
		"rollout-2026-03-06T09-00-00-"+f4+".jsonl"),
		fallbackLines(f4, "", "exec", "", "nojs work"))
	writeZstArchived(t, dir, f3, "zst content")

	base := time.Now().Add(-4 * time.Hour)
	setMTime(t, filepath.Join(dir, "sessions", "2026", "03", "04",
		"rollout-2026-03-04T09-00-00-"+f1+".jsonl"), base)
	setMTime(t, filepath.Join(dir, "archived_sessions",
		"rollout-2026-03-07T09-00-00-"+f3+".jsonl.zst"), base.Add(2*time.Hour))
	setMTime(t, filepath.Join(dir, "sessions", "2026", "03", "06",
		"rollout-2026-03-06T09-00-00-"+f4+".jsonl"), base.Add(3*time.Hour))
}

// writeZstArchived writes one archived_sessions rollout compressed with
// zstd, like real archived Codex sessions.
func writeZstArchived(t *testing.T, dir, id, text string) {
	t.Helper()
	path := filepath.Join(dir, "archived_sessions",
		"rollout-2026-03-07T09-00-00-"+id+".jsonl.zst")
	writeZstRolloutFile(t, path, fallbackLines(id, "dev", "exec", "user", text))
}

func writeRolloutFile(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZstRolloutFile(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte(joinLines(lines))); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func setMTime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// fallbackLines is a session_meta + user + assistant stream for the
// filesystem fallback listing, with configurable source/thread_source.
func fallbackLines(id, branch string, source any, threadSource, text string) []string {
	const ts = "2026-03-07T09:00:00Z"
	meta := map[string]any{
		"session_id":    id,
		"cwd":           "/tmp/proj",
		"cli_version":   "0.42.0",
		"source":        source,
		"thread_source": threadSource,
		"git": map[string]any{
			"repository_url": "https://github.com/example/fb.git",
			"branch":         branch,
			"commit_hash":    "def456abc",
		},
	}
	return []string{
		rolloutLine(ts, "session_meta", meta),
		rolloutLine(ts, "turn_context", map[string]any{"model": "gpt-5.2", "cwd": "/tmp/proj"}),
		rolloutLine(ts, "response_item", userMessage(textItem("input_text", text))),
		rolloutLine(ts, "response_item", assistantMessage(textItem("output_text", "ok: "+text))),
	}
}

func testAdapter(dir string, logs *[]string) *Adapter {
	return New(dir, WithLogger(func(format string, args ...any) {
		if logs != nil {
			*logs = append(*logs, fmt.Sprintf(format, args...))
		}
	}))
}

func listAll(ctx context.Context, a *Adapter, includeArchived bool, limit int) []string {
	metas, err := a.List(ctx, listOpts(includeArchived, limit))
	if err != nil {
		panic(err)
	}
	ids := make([]string, len(metas))
	for i, m := range metas {
		ids[i] = m.ID
	}
	return ids
}
