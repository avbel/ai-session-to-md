package antigravity

import (
	"database/sql"
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/sqlitero"
)

const (
	idMain    = "11111111-2222-4333-8444-555555555555"
	idOther   = "22222222-2222-4333-8444-555555555555"
	idChild   = "33333333-2222-4333-8444-555555555555"
	idDesktop = "44444444-2222-4333-8444-555555555555"
	idNoLog   = "55555555-2222-4333-8444-555555555555"
)

// rec builds one transcript line in agy's shape; fields maps straight onto
// the JSON object.
func rec(step int, source, typ, status string, fields map[string]any) string {
	obj := map[string]any{
		"step_index": step, "source": source, "type": typ, "status": status,
		"created_at": "2026-11-24T12:00:00Z",
	}
	maps.Copy(obj, fields)
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func userInput(step int, text string) string {
	return rec(step, "USER_EXPLICIT", typeUserInput, "DONE", map[string]any{
		"content": "<USER_REQUEST>\n" + text + "\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-24T16:00:00+04:00.\n</ADDITIONAL_METADATA>",
	})
}

func planner(step int, content string, calls ...map[string]any) string {
	fields := map[string]any{"content": content, "model": "gemini-3.8-flash-high"}
	if content == "" {
		delete(fields, "content")
	}
	if len(calls) > 0 {
		fields["tool_calls"] = calls
	}
	return rec(step, sourceModel, typePlanner, "DONE", fields)
}

func call(name string, args map[string]any) map[string]any {
	args["toolAction"], args["toolSummary"] = "ui", "ui"
	return map[string]any{"name": name, "args": args}
}

func result(step int, typ, status, content string) string {
	return rec(step, sourceModel, typ, status, map[string]any{
		"content": "Created At: 2026-11-24T12:00:01Z\nCompleted At: 2026-11-24T12:00:02Z\n" + content,
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeTranscript writes a conversation log under root/app; name picks the
// full or the truncated log.
func writeTranscript(t *testing.T, root, app, id, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(logsDir(root, app, id), name)
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

type sumRow struct {
	id, title, uris, modified, status, parent, app string
	depth                                          int
	notIdle                                        bool
}

// writeIndex creates conversation_summaries.db with agy's column types, so
// the DATETIME handling is exercised as in the real file.
func writeIndex(t *testing.T, root string, rows ...sumRow) {
	t.Helper()
	path := filepath.Join(root, cliApp, summariesFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqlitero.URI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	const schema = "CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\"," +
		"`preview` text NOT NULL DEFAULT \"\",`step_count` integer NOT NULL DEFAULT 0,`last_modified_time` datetime NOT NULL," +
		"`workspace_uris` text NOT NULL,`status` text NOT NULL DEFAULT \"\",`source` text NOT NULL DEFAULT \"\"," +
		"`project_id` text NOT NULL DEFAULT \"\",`agent_name` text NOT NULL DEFAULT \"\",`parent_conversation_id` text NOT NULL DEFAULT \"\"," +
		"`nesting_depth` integer NOT NULL DEFAULT 0,`battle_id` text NOT NULL DEFAULT \"\",`winning_conversation_id` text NOT NULL DEFAULT \"\"," +
		"`not_fully_idle` numeric NOT NULL DEFAULT false,`killed` numeric NOT NULL DEFAULT false,`last_user_input_time` datetime NOT NULL," +
		"`last_user_input_step_index` integer NOT NULL DEFAULT -1,`app_data_dir` text NOT NULL DEFAULT \"\", raw_summary BLOB," +
		" group_id TEXT NOT NULL DEFAULT '',PRIMARY KEY (`conversation_id`))"
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO conversation_summaries (conversation_id, title, last_modified_time, workspace_uris,
			status, parent_conversation_id, nesting_depth, not_fully_idle, last_user_input_time, app_data_dir)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, '0001-01-01 00:00:00+00:00', ?)`,
			r.id, r.title, r.modified, r.uris, r.status, r.parent, r.depth, r.notIdle, r.app)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// fileURI encodes a local path the way agy writes workspace_uris.
func fileURI(p string) string {
	slash := filepath.ToSlash(p)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}
