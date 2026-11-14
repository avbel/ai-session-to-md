package handoff

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

var update = flag.Bool("update", false, "rewrite golden files")

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "handoff", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test -update ./internal/handoff/)", path, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

func fullBuildOptions() BuildOptions {
	return BuildOptions{
		Harness:     session.HarnessCodex,
		SessionID:   "sess-0123abcd",
		Title:       "Fix huge jsonl lines",
		GeneratedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
		Host:        "mbp.local",
		ToolVersion: "0.1.0",
		Model:       "openai/gpt-5",
		Status: session.Status{
			Text:             "stopped during tool call Bash",
			PendingToolName:  "Bash",
			PendingToolInput: `{"command":"go test ./internal/jsonl/..."}`,
			LastUser:         "make the reader handle 2 MB lines",
			LastAssistant:    "On it — patching the reader now.",
		},
		Todos: []session.Todo{
			{Content: "write the reader", Status: "completed"},
			{Content: "fix the 2 MB test", Status: "in_progress"},
			{Content: "wire into the codex adapter", Status: "pending"},
		},
		Repo: RepoState{
			Source:          "live",
			RepoURL:         "github.com/avbel/ai-session-to-md",
			Branch:          "main",
			Head:            "abc123def456",
			HeadSubject:     "add jsonl reader",
			StatusLines:     []string{" M internal/jsonl/reader.go", "?? internal/jsonl/huge_test.go"},
			DiffStatSummary: " 1 file changed, 12 insertions(+)",
			DiffStatFiles:   []string{" internal/jsonl/reader.go | 12 ++++++"},
			Ahead:           2,
			Behind:          0,
			Warnings: []string{
				"Uncommitted changes are NOT in this file. Commit and push (e.g. a WIP branch) before switching machines.",
				"2 commits not pushed.",
			},
		},
		FilesTouched: []string{"internal/jsonl/reader.go", "internal/jsonl/render_test.go"},
		Summary:      goodSummary,
		History: []HistoryEntry{{
			Harness: "claude-code", SessionID: "hop-aaaa",
			GeneratedAt: "2026-11-13T09:00:00Z", Host: "macmini",
			TLDR: "Start the reader work.",
		}},
	}
}

func TestRenderGoldenFull(t *testing.T) {
	doc, err := Build(fullBuildOptions())
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "full.md", doc.Content)
	if doc.Meta.TLDR != "Make the jsonl reader handle huge lines; stopped after tests failed; rerun go test next." {
		t.Errorf("meta tldr wrong: %q", doc.Meta.TLDR)
	}
	if doc.Meta.Raw {
		t.Error("full mode must not set raw")
	}
}

func TestRenderGoldenRaw(t *testing.T) {
	o := BuildOptions{
		Harness:     session.HarnessClaudeCode,
		SessionID:   "hop-bbbb",
		Title:       "Raw transcript run -- with dashes",
		GeneratedAt: time.Date(2026, 11, 14, 13, 30, 0, 0, time.UTC),
		Host:        "mbp.local",
		ToolVersion: "0.1.0",
		Raw:         true,
		Status: session.Status{
			Text:     "turn completed",
			LastUser: "run the tests\nplease",
		},
		Repo:         RepoState{Source: "recorded by harness", Branch: "dev"},
		FilesTouched: []string{"internal/reduce/reduce.go"},
		Transcript:   "[#1 user 2026-11-14T13:00Z] run the tests\n[#2 assistant] ```go\nfunc x() {}\n```\n[#3 tool_result] ok",
	}
	doc, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "raw.md", doc.Content)
	if !doc.Meta.Raw {
		t.Error("raw mode must set the raw flag")
	}
	if doc.Meta.TLDR != o.Title {
		t.Errorf("raw mode tldr must be the title, got %q", doc.Meta.TLDR)
	}
	if strings.Contains(doc.Content, "## Summary") {
		t.Error("raw mode must not have a Summary section")
	}
}

func TestRenderPendingToolMultiline(t *testing.T) {
	o := fullBuildOptions()
	o.Status.PendingToolInput = "{\"command\":\"go test ./...\",\n\"cwd\":\"/repo\"}"
	o.Status.Text = "stopped during tool call Bash"
	doc, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Content, "- **Pending tool call:** `Bash` —\n") {
		t.Errorf("multi-line input must use a block fence:\n%s", doc.Content)
	}
}

func TestFenceLongerThanContentBackticks(t *testing.T) {
	o := fullBuildOptions()
	o.Status.LastAssistant = "here ``` is inline and ```` a run"
	o.Status.LastUser = "plain"
	doc, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Content, "  `````text\n") {
		t.Errorf("the verbatim fence must exceed the content's longest run:\n%s", doc.Content)
	}
	if !strings.Contains(doc.Content, "  ````text\n") {
		t.Errorf("the verbatim fence for plain content stays at the 4-run minimum:\n%s", doc.Content)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	if err := WriteFile(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second\n" {
		t.Errorf("got %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode must be 0644, got %v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temp files must not remain: %v", entries)
	}
}

func TestWriteFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.md")
	if err := os.WriteFile(target, []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	err := WriteFile(link, []byte("through\n"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlinked target must be refused, got %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "real\n" {
		t.Error("the symlink target must be untouched")
	}
}

func TestOutputNamesAndDisplay(t *testing.T) {
	if HarnessDisplay(session.HarnessOpenCode) != "OpenCode" {
		t.Errorf("got %q", HarnessDisplay(session.HarnessOpenCode))
	}
	if HarnessDisplay(session.Harness("weird")) != "weird" {
		t.Error("unknown harness falls back to its raw name")
	}
}

func TestMetaFromGoldenFullParses(t *testing.T) {
	doc, err := Build(fullBuildOptions())
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := ParseMeta(FirstLine(doc.Content))
	if !ok {
		t.Fatal("meta of a built document must parse from the first line")
	}
	if meta.SessionID != "sess-0123abcd" || meta.Format != 1 || len(meta.History) != 1 {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if !strings.HasPrefix(doc.Content, MetaPrefix) {
		t.Error("the meta comment must be the first line")
	}
}
