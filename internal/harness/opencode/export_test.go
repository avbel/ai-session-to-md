package opencode

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// buildFallbackDB creates a database without the v2 tables, i.e. an
// unrecognized OpenCode version.
func buildFallbackDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fallback db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE other (x TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return path, db
}

func TestListFallbackUsageError(t *testing.T) {
	path, db := buildFallbackDB(t)
	_ = db.Close()
	a := New(filepath.Dir(path))
	_, err := a.List(t.Context(), listOptAll())
	if err == nil || err.Error() != "OpenCode storage not recognised; pass a session id" {
		t.Fatalf("err = %v, want the usage error", err)
	}
}

func TestLoadFallbackInvalidIDNoExec(t *testing.T) {
	path, db := buildFallbackDB(t)
	_ = db.Close()
	a := New(filepath.Dir(path), WithCommand("definitely-not-opencode-xyz"))
	_, err := a.LoadDetailed(t.Context(), session.Meta{ID: "abc"})
	if err == nil || !strings.Contains(err.Error(), "invalid opencode session id") {
		t.Fatalf("err = %v, want invalid id error before any exec", err)
	}
}

func TestParseExportFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "opencode", "export.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, perr := parseExport(raw)
	if perr != nil {
		t.Fatalf("parseExport: %v", perr)
	}
	if doc.Info.Title != "Exported session" || doc.Info.Directory != "/proj/handoff" {
		t.Fatalf("info = %+v", doc.Info)
	}
	if len(doc.Messages) != 5 {
		t.Fatalf("messages = %d, want 5", len(doc.Messages))
	}
}

func TestBuildExportFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "opencode", "export.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	a := New(t.TempDir())
	res, err := a.buildExport(t.Context(), session.Meta{ID: "ses_01export"}, raw)
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	s := res.Session
	if s.Title != "Exported session" || s.Cwd != "/proj/handoff" {
		t.Fatalf("meta = %+v", s.Meta)
	}
	if s.Model != "openai/gpt-5.2" {
		t.Fatalf("model = %q, want openai/gpt-5.2", s.Model)
	}
	if s.HarnessVer != "0.4.12" {
		t.Fatalf("HarnessVer = %q", s.HarnessVer)
	}
	want := "user,assistant,reasoning,tool_call,tool_result,compaction_summary,user"
	if got := kindsString(s); got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
	if s.Turns[0].Text != "summarize the repo [file: PLAN.md]" {
		t.Fatalf("user text = %q", s.Turns[0].Text)
	}
	if s.Turns[4].Text != "plan contents" || s.Turns[4].ToolID != "part_1" {
		t.Fatalf("tool result = %+v", s.Turns[4])
	}
	for _, turn := range s.Turns {
		if strings.Contains(turn.Text, "sk-aaa") {
			t.Fatalf("synthetic secret leaked: %q", turn.Text)
		}
	}
	if !res.Info.HasFinish || res.Info.Finish != "" {
		t.Fatalf("info = %+v, want HasFinish with empty finish", res.Info)
	}
	// The finish of the fixture's last assistant message is null, which per
	// §8 rule order reports "assistant turn incomplete" before the
	// unanswered-user rule can apply.
	if st := session.Derive(s, res.Info); st.Text != "assistant turn incomplete" {
		t.Fatalf("status = %q", st.Text)
	}
}

// TestBuildExportTypedModelField reproduces the data.model object bug class:
// an export whose info.model is a {"id","providerID"} object (and whose time
// fields are objects) must parse instead of failing the whole document.
func TestBuildExportTypedModelField(t *testing.T) {
	raw := `{
	  "info": {
	    "id": "ses_typedmod",
	    "title": "typed model",
	    "directory": "/proj",
	    "model": {"id": "gpt-5.2", "providerID": "openai"},
	    "version": "0.5.0",
	    "time_updated": {"something": true},
	    "time_archived": null
	  },
	  "messages": [
	    {"type": "user", "text": "hello"},
	    {"type": "user", "text": "", "time": 1},
	    {"type": "assistant", "content": [{"type": "text", "text": "hi"}], "finish": "stop"}
	  ]
	}`
	a := New(t.TempDir())
	res, err := a.buildExport(t.Context(), session.Meta{ID: "ses_typedmod"}, []byte(raw))
	if err != nil {
		t.Fatalf("buildExport with object-typed model: %v", err)
	}
	if res.Session.Model != "openai/gpt-5.2" {
		t.Fatalf("model = %q, want openai/gpt-5.2", res.Session.Model)
	}
	if res.Session.Archived {
		t.Error("null time_archived must not flag archived")
	}
	// The empty user row is dropped silently, not counted as unparseable.
	if res.Session.Skipped != 0 {
		t.Fatalf("Skipped = %d, want 0 for empty user rows", res.Session.Skipped)
	}
	if len(res.Session.Turns) != 2 || res.Session.Turns[0].Text != "hello" {
		t.Fatalf("turns = %+v", res.Session.Turns)
	}
}

func TestParseExportShapeMismatch(t *testing.T) {
	for _, bad := range []string{`{"info":{}}`, `[]`, `not json`, `{"messages":"nope"}`} {
		if _, err := parseExport([]byte(bad)); err == nil {
			t.Errorf("parseExport(%q) = nil error, want shape/parse error", bad)
		}
	}
}

func TestLoadExportUnsupportedVersionMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/echo as a stand-in binary")
	}
	path, db := buildFallbackDB(t)
	_ = db.Close()
	a := New(filepath.Dir(path), WithCommand("/bin/echo"))
	_, err := a.LoadDetailed(t.Context(), session.Meta{ID: "ses_01export"})
	if err == nil || !strings.HasPrefix(err.Error(), "unsupported OpenCode version ") {
		t.Fatalf("err = %v, want unsupported-version message", err)
	}
}

func TestExportRunFailure(t *testing.T) {
	path, db := buildFallbackDB(t)
	_ = db.Close()
	a := New(filepath.Dir(path), WithCommand("definitely-not-opencode-xyz"))
	_, err := a.LoadDetailed(t.Context(), session.Meta{ID: "ses_01export"})
	if err == nil || !strings.Contains(err.Error(), "opencode session export ses_01export") {
		t.Fatalf("err = %v, want wrapped export failure", err)
	}
}

func TestParseHelpers(t *testing.T) {
	if got := resultText([]byte(`"plain"`)); got != "plain" {
		t.Errorf("resultText string = %q", got)
	}
	if got := resultText([]byte(`[{"text":"a"},{"text":"b"}]`)); got != "a\nb" {
		t.Errorf("resultText array = %q", got)
	}
	if got := compactJSON([]byte("{ \"a\": 1, \"b\":[1,2] }")); got != `{"a":1,"b":[1,2]}` {
		t.Errorf("compactJSON = %q", got)
	}
	if got := modelJSON(`{"id":"gpt-5","providerID":"openai"}`); got != "openai/gpt-5" {
		t.Errorf("modelJSON object = %q", got)
	}
	if got := modelJSON(`"anthropic/claude-x"`); got != "anthropic/claude-x" {
		t.Errorf("modelJSON quoted string = %q", got)
	}
	if got := modelJSON("anthropic/claude-x"); got != "anthropic/claude-x" {
		t.Errorf("modelJSON raw = %q", got)
	}
	if got := fileNames([]byte(`["a.go",{"name":"b.ts"},{"path":"c.md"}]`)); !reflect.DeepEqual(got, []string{"a.go", "b.ts", "c.md"}) {
		t.Errorf("fileNames = %v", got)
	}
	if got := validSessionID("ses_01export"); !got {
		t.Errorf("validSessionID positive failed")
	}
	for _, bad := range []string{"abc", "-ses_01export", "ses/01", "", "with.dot!!"} {
		if validSessionID(bad) {
			t.Errorf("validSessionID(%q) = true, want false", bad)
		}
	}
}
