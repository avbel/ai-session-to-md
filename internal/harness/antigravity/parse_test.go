package antigravity

import (
	"bufio"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

func TestUserText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"request with metadata",
			"<USER_REQUEST>\nfix the test\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nlocal time\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nmodel\n</USER_SETTINGS_CHANGE>",
			"fix the test"},
		{"two requests", "<USER_REQUEST>a</USER_REQUEST>x<USER_REQUEST>b</USER_REQUEST>", "a\n\nb"},
		{"truncated, closing tag lost", "<USER_REQUEST>\n# Brief\nlong diff\n<truncated 7690 bytes>", "# Brief\nlong diff\n<truncated 7690 bytes>"},
		{"no request tag", "plain text\n<ADDITIONAL_METADATA>\nx\n</ADDITIONAL_METADATA>", "plain text"},
		{"metadata only", "<ADDITIONAL_METADATA>x</ADDITIONAL_METADATA>", ""},
	}
	for _, tc := range cases {
		if got := userText(tc.in); got != tc.want {
			t.Errorf("%s: userText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResultText(t *testing.T) {
	in := "Created At: 2026-09-07T20:07:28+04:00\nCompleted At: 2026-09-07T20:07:28+04:00\nCreated At: 2026-09-07T20:07:28+04:00\nFile Path: x\nCreated At: kept"
	if got, want := resultText(in), "File Path: x\nCreated At: kept"; got != want {
		t.Errorf("resultText = %q, want %q", got, want)
	}
	if got := resultText("Created At: 2026-11-24T12:00:00Z"); got != "" {
		t.Errorf("stamp-only result = %q, want empty", got)
	}
}

func TestUnwrapArgs(t *testing.T) {
	args := map[string]json.RawMessage{
		"Cwd":               json.RawMessage(`"\"/Users/x/p\""`),
		"WaitMsBeforeAsync": json.RawMessage(`"500"`),
		"IsArtifact":        json.RawMessage(`"true"`),
		"Description":       json.RawMessage(`"Add a rule"`),
		"EndLine":           json.RawMessage(`25`),
	}
	got := unwrapArgs(args)
	want := map[string]string{
		"Cwd": `"/Users/x/p"`, "WaitMsBeforeAsync": `500`, "IsArtifact": `true`,
		"Description": `"Add a rule"`, "EndLine": `25`,
	}
	for k, w := range want {
		if string(got[k]) != w {
			t.Errorf("%s = %s, want %s", k, got[k], w)
		}
	}
}

func TestParseTasks(t *testing.T) {
	in := "# Tasks\n- `[x]` Setup `tsconfig.json`\n  - `[/]` Implement extractor\n- [ ] Test on archive\n* [X] Done too\nnot a task\n- [?] odd box\n"
	got := parseTasks(bufio.NewScanner(strings.NewReader(in)))
	want := []session.Todo{
		{Content: "Setup `tsconfig.json`", Status: "completed"},
		{Content: "Implement extractor", Status: "in_progress"},
		{Content: "Test on archive", Status: "pending"},
		{Content: "Done too", Status: "completed"},
	}
	if len(got) != len(want) {
		t.Fatalf("todos = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("todo %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAnnotationTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, cliApp, "annotations", idMain+".pbtxt"), `title:"Caf\303\251 \"review\""`+"\n")
	a := New(root)
	if got, want := a.annotationTitle(cliApp, idMain), `Café "review"`; got != want {
		t.Errorf("annotationTitle = %q, want %q", got, want)
	}
	if got := a.annotationTitle(cliApp, idOther); got != "" {
		t.Errorf("missing annotation = %q, want empty", got)
	}
}

func TestWorkspacePaths(t *testing.T) {
	got := workspacePaths(`["file:///Users/x/my%20proj","https://example.com/x","file:///tmp"]`)
	want := []string{filepath.FromSlash("/Users/x/my proj"), filepath.FromSlash("/tmp")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("workspacePaths = %q, want %q", got, want)
	}
	if got := workspacePaths(""); got != nil {
		t.Errorf("empty uris = %q", got)
	}
	if runtime.GOOS == "windows" {
		if got := fileURIPath("file:///C:/Users/x"); got != `C:\Users\x` {
			t.Errorf("windows drive uri = %q", got)
		}
	}
}

func TestParseSummaryTime(t *testing.T) {
	if got := parseSummaryTime("2026-11-24 12:22:21.434501+00:00"); got.IsZero() || got.UTC().Hour() != 12 || got.Nanosecond() != 434501000 {
		t.Errorf("parsed = %v", got)
	}
	if got := parseSummaryTime("0001-01-01 00:00:00+00:00"); !got.IsZero() {
		t.Errorf("zero datetime = %v, want zero time", got)
	}
}

// buildTurns runs the builder over transcript lines through readRecords, so
// the step ordering and argument unwrapping apply as in Load.
func buildTurns(t *testing.T, lines ...string) *builder {
	t.Helper()
	path := filepath.Join(t.TempDir(), fullLog)
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	recs, _, err := readRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	b := newBuilder(filepath.Join(t.TempDir(), "brain", idMain))
	b.addAll(recs)
	return b
}

func TestBuilderPairsResultsInOrder(t *testing.T) {
	b := buildTurns(t,
		userInput(0, "read both"),
		planner(1, "", call("view_file", map[string]any{"AbsolutePath": "/a"}), call("view_file", map[string]any{"AbsolutePath": "/b"})),
		result(2, "VIEW_FILE", "DONE", "A body"),
		result(3, "VIEW_FILE", "ERROR", "B failed"),
		planner(4, "Read both files."),
	)
	var calls, results []session.Turn
	for _, turn := range b.turns {
		switch turn.Kind {
		case session.ToolCall:
			calls = append(calls, turn)
		case session.ToolResult:
			results = append(results, turn)
		}
	}
	if len(calls) != 2 || len(results) != 2 {
		t.Fatalf("calls=%d results=%d: %+v", len(calls), len(results), b.turns)
	}
	if results[0].ToolID != calls[0].ToolID || results[0].Text != "A body" || results[0].IsError {
		t.Errorf("first result = %+v", results[0])
	}
	if results[1].ToolID != calls[1].ToolID || !results[1].IsError {
		t.Errorf("second result = %+v", results[1])
	}
	if calls[0].Input != `{"AbsolutePath":"/a"}` {
		t.Errorf("call input = %s, want UI fields dropped", calls[0].Input)
	}
	if got := session.Derive(session.Session{Turns: b.turns}, session.Info{}).Text; got != "turn completed" {
		t.Errorf("status = %q", got)
	}
}

// A failed call stops agy's batch: the sibling call never runs, and the
// next planner step must not leave it open as the call the session is in.
func TestBuilderClosesDroppedCalls(t *testing.T) {
	b := buildTurns(t,
		userInput(0, "remember and check"),
		planner(1, "", call("call_mcp_tool", map[string]any{"ToolName": "remember"}), call("run_command", map[string]any{"CommandLine": "ls"})),
		result(2, "GENERIC", "ERROR", "permission denied"),
		planner(3, "The memory tool was denied."),
	)
	if got := session.Derive(session.Session{Turns: b.turns}, session.Info{}).Text; got != "turn completed" {
		t.Errorf("status = %q, want turn completed", got)
	}
}

func TestBuilderRunningResult(t *testing.T) {
	mid := buildTurns(t,
		userInput(0, "start the server"),
		planner(1, "", call("run_command", map[string]any{"CommandLine": "npm start"})),
		result(2, "RUN_COMMAND", "RUNNING", "started"),
		planner(3, "The server is running."),
	)
	if got := session.Derive(session.Session{Turns: mid.turns}, session.Info{}).Text; got != "turn completed" {
		t.Errorf("RUNNING mid-transcript: status = %q, want turn completed", got)
	}
	last := buildTurns(t,
		userInput(0, "run the tests"),
		planner(1, "", call("run_command", map[string]any{"CommandLine": "go test ./..."})),
		result(2, "RUN_COMMAND", "RUNNING", "running"),
	)
	st := session.Derive(session.Session{Turns: last.turns}, session.Info{})
	if st.Text != "stopped during tool call run_command" || !strings.Contains(st.PendingToolInput, "go test") {
		t.Errorf("RUNNING last: status = %+v", st)
	}
}

func TestBuilderOrdersBySteps(t *testing.T) {
	// The planner step is written after its own results, as agy does.
	b := buildTurns(t,
		userInput(0, "read"),
		result(2, "GENERIC", "DONE", "a"),
		planner(1, "", call("view_file", map[string]any{"AbsolutePath": "/a"})),
		planner(3, "done"),
	)
	for _, turn := range b.turns {
		if turn.Kind == session.ToolResult && (turn.ToolID != "1-0" || turn.Text != "a") {
			t.Errorf("result = %+v, want the step-2 output paired with call 1-0", turn)
		}
	}
}

func TestBuilderFilesAndModel(t *testing.T) {
	brain := filepath.Join(t.TempDir(), "brain", idMain)
	recs := []record{
		{Type: typeUserInput, Content: "<USER_REQUEST>x</USER_REQUEST>"},
		{Type: typePlanner, Source: sourceModel, Model: "gemini-3.8-flash-high", ToolCalls: []toolCall{
			{Name: "write_to_file", Args: map[string]json.RawMessage{"TargetFile": json.RawMessage(`"/repo/a.go"`)}},
			{Name: "replace_file_content", Args: map[string]json.RawMessage{"TargetFile": json.RawMessage(`"/repo/a.go"`)}},
			{Name: "write_to_file", Args: map[string]json.RawMessage{"TargetFile": json.RawMessage(`"/repo/plan.md"`), "IsArtifact": json.RawMessage(`true`)}},
			{Name: "write_to_file", Args: map[string]json.RawMessage{"TargetFile": json.RawMessage(`"` + filepath.ToSlash(filepath.Join(brain, "task.md")) + `"`)}},
			{Name: "view_file", Args: map[string]json.RawMessage{"AbsolutePath": json.RawMessage(`"/repo/b.go"`)}},
		}},
	}
	b := newBuilder(brain)
	b.addAll(recs)
	if len(b.files) != 1 || b.files[0] != "/repo/a.go" {
		t.Errorf("files = %q, want only /repo/a.go", b.files)
	}
	if b.model != "gemini-3.8-flash-high" {
		t.Errorf("model = %q", b.model)
	}
}

func TestEndsOnError(t *testing.T) {
	failed := []record{
		{Type: typeUserInput},
		{Type: typeError, Source: "SYSTEM"},
		{Type: "EPHEMERAL_MESSAGE", Source: "SYSTEM"},
	}
	if !endsOnError(failed) {
		t.Error("trailing model error not detected")
	}
	retried := []record{
		{Type: typeUserInput},
		{Type: typeError, Source: "SYSTEM"},
		{Type: typePlanner, Source: sourceModel},
	}
	if endsOnError(retried) {
		t.Error("an error followed by an answer reads as failed")
	}
}

func TestReadRecordsSkipsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), fullLog)
	writeFile(t, path, userInput(0, "x")+"\nnot json\n{\"step_index\":1}\n")
	recs, skipped, err := readRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || skipped != 2 {
		t.Errorf("recs=%d skipped=%d", len(recs), skipped)
	}
}
