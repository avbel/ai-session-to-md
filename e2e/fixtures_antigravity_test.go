//go:build e2e

package e2e

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	agyMainID  = "aaaaaaaa-1111-4222-8333-444444444444"
	agyChildID = "bbbbbbbb-1111-4222-8333-444444444444"
	agyOtherID = "cccccccc-1111-4222-8333-444444444444"
)

func (e *testEnv) antigravityDir() string {
	return filepath.Join(e.home, ".gemini")
}

// writeAntigravityStore builds ~/.gemini with the agy index and three
// conversations: the main one in the repo, a newer subagent of it and a newer
// one in another project. If the filters regressed, --latest would pick one
// of the newer two.
func writeAntigravityStore(t *testing.T, e *testEnv) {
	t.Helper()
	cli := filepath.Join(e.antigravityDir(), "antigravity-cli")
	target := filepath.Join(e.repo, "pkg", "agy", "agy.go")
	writeE2EFile(t, target, "package agy\n")
	writeAgyTranscript(t, cli, agyMainID,
		agyRecord(0, "USER_EXPLICIT", "USER_INPUT", "DONE", map[string]any{
			"content": "<USER_REQUEST>\nPort the parser to agy\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nlocal time\n</ADDITIONAL_METADATA>",
		}),
		agyRecord(1, "MODEL", "PLANNER_RESPONSE", "DONE", map[string]any{
			"content": "Writing the parser.", "model": "gemini-3.8-flash-high",
			"tool_calls": []any{map[string]any{"name": "write_to_file", "args": map[string]any{
				"TargetFile": target, "CodeContent": "package agy", "toolAction": "Writing", "toolSummary": "Write",
			}}},
		}),
		agyRecord(2, "MODEL", "CODE_ACTION", "DONE", map[string]any{"content": "Created At: 2026-11-24T12:00:00Z\nCreated file."}),
		agyRecord(3, "MODEL", "PLANNER_RESPONSE", "DONE", map[string]any{
			"tool_calls": []any{map[string]any{"name": "run_command", "args": map[string]any{
				"CommandLine": "go test ./pkg/agy", "Cwd": e.repo,
			}}},
		}),
	)
	writeE2EFile(t, filepath.Join(cli, "brain", agyMainID, "task.md"), "- `[x]` port the parser\n- `[/]` run the agy tests\n")
	writeE2EFile(t, filepath.Join(cli, "annotations", agyMainID+".pbtxt"), `title:"Antigravity E2E session"`+"\n")
	writeAgyTranscript(t, cli, agyChildID, agyRecord(0, "USER_EXPLICIT", "USER_INPUT", "DONE", map[string]any{"content": "CHILD ONLY"}))
	writeAgyTranscript(t, cli, agyOtherID, agyRecord(0, "USER_EXPLICIT", "USER_INPUT", "DONE", map[string]any{"content": "OTHER PROJECT"}))
	writeAgyIndex(t, filepath.Join(cli, "conversation_summaries.db"), [][]any{
		{agyMainID, agyWorkspaces(e.repo), "2026-11-24 12:00:00+00:00", "", 0},
		{agyChildID, agyWorkspaces(e.repo), "2026-11-24 13:00:00+00:00", agyMainID, 1},
		{agyOtherID, agyWorkspaces(filepath.Join(e.home, "elsewhere")), "2026-11-24 14:00:00+00:00", "", 0},
	})
}

func agyRecord(step int, source, typ, status string, fields map[string]any) string {
	obj := map[string]any{"step_index": step, "source": source, "type": typ, "status": status, "created_at": "2026-11-24T12:00:00Z"}
	for k, v := range fields {
		obj[k] = v
	}
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func agyWorkspaces(dirs ...string) string {
	uris := make([]string, len(dirs))
	for i, d := range dirs {
		p := filepath.ToSlash(d)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		uris[i] = (&url.URL{Scheme: "file", Path: p}).String()
	}
	b, _ := json.Marshal(uris)
	return string(b)
}

func writeAgyTranscript(t *testing.T, appDir, id string, lines ...string) {
	t.Helper()
	path := filepath.Join(appDir, "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
	writeE2EFile(t, path, strings.Join(lines, "\n")+"\n")
}

func writeE2EFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeAgyIndex creates the agy conversation index with the columns the
// adapter reads; each row is id, workspace_uris, last_modified_time,
// parent_conversation_id, nesting_depth.
func writeAgyIndex(t *testing.T, path string, rows [][]any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open agy index: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE `conversation_summaries` (`conversation_id` text, `title` text NOT NULL DEFAULT \"\"," +
		" `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `status` text NOT NULL DEFAULT \"CASCADE_RUN_STATUS_IDLE\"," +
		" `parent_conversation_id` text NOT NULL DEFAULT \"\", `nesting_depth` integer NOT NULL DEFAULT 0," +
		" `not_fully_idle` numeric NOT NULL DEFAULT false, `killed` numeric NOT NULL DEFAULT false," +
		" `app_data_dir` text NOT NULL DEFAULT \"antigravity-cli\", PRIMARY KEY (`conversation_id`))"); err != nil {
		t.Fatalf("create agy index: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO conversation_summaries (conversation_id, workspace_uris, last_modified_time,
			parent_conversation_id, nesting_depth) VALUES (?,?,?,?,?)`, r...); err != nil {
			t.Fatalf("insert agy index row: %v", err)
		}
	}
}
