package reduce

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/avbel/ai-session-to-md/internal/session"
)

func TestRenderTurnExactStrings(t *testing.T) {
	ts := time.Date(2026, 11, 14, 10, 22, 3, 0, time.UTC)
	cases := []struct {
		num  int
		turn session.Turn
		want string
	}{
		{12, session.Turn{Kind: session.UserText, Time: ts, Text: "text"}, "[#12 user 2026-11-14T10:22Z] text\n"},
		{13, session.Turn{Kind: session.AssistantText, Text: "text"}, "[#13 assistant] text\n"},
		{14, session.Turn{Kind: session.ToolCall, ToolName: "Bash", ToolID: "toolu_x", Input: `{"command":"go test ./..."}`},
			"[#14 tool_call Bash id=toolu_x] {\"command\":\"go test ./...\"}\n"},
		{15, session.Turn{Kind: session.ToolResult, ToolID: "toolu_x", IsError: true, Text: "output…"},
			"[#15 tool_result id=toolu_x ERROR] output…\n"},
		{16, session.Turn{Kind: session.Reasoning, Text: "text"}, "[#16 reasoning] text\n"},
		{17, session.Turn{Kind: session.CompactionSummary, Text: "text"}, "[#17 compaction_summary] text\n"},
		{18, session.Turn{Kind: session.Interrupted}, "[#18 interrupted]\n"},
		{19, session.Turn{Kind: session.UserText, Text: "hi"}, "[#19 user] hi\n"},
		{20, session.Turn{Kind: session.ToolCall, ToolName: "Bash", Input: "{}"}, "[#20 tool_call Bash] {}\n"},
		{21, session.Turn{Kind: session.ToolResult, Text: "ok"}, "[#21 tool_result] ok\n"},
	}
	for _, tc := range cases {
		got := renderTurn(tc.num, tc.turn, tailLimits(), 0)
		if got != tc.want {
			t.Errorf("renderTurn(#%d) =\n%q\nwant\n%q", tc.num, got, tc.want)
		}
	}
}

func TestRenderUserTimestampConvertedToUTC(t *testing.T) {
	ts := time.Date(2026, 11, 14, 17, 22, 0, 0, time.FixedZone("CET", 7*3600))
	turn := session.Turn{Kind: session.UserText, Time: ts, Text: "hi"}
	want := "[#1 user 2026-11-14T10:22Z] hi\n"
	if got := renderTurn(1, turn, tailLimits(), 0); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTextHeadTailCut(t *testing.T) {
	text := strings.Repeat("x", 25000)
	turn := session.Turn{Kind: session.AssistantText, Text: text}
	got := renderTurn(1, turn, tailLimits(), 0)
	if !strings.HasPrefix(got, "[#1 assistant] xxxxx") {
		t.Error("block should start with the header and the head of the text")
	}
	if !strings.HasSuffix(got, "xxxxx\n") {
		t.Error("block should end with the tail of the text")
	}
	if n := strings.Count(got, textCutMarker); n != 1 {
		t.Errorf("want 1 cut marker, got %d", n)
	}
	textPart := strings.TrimSuffix(strings.SplitN(got, "] ", 2)[1], "\n")
	if len(textPart) != 20000 {
		t.Errorf("text capped at %d bytes, want 20000", len(textPart))
	}
	if !utf8.ValidString(textPart) {
		t.Error("cut text must stay valid UTF-8")
	}
}

func TestRenderToolResultLineTruncation(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	turn := session.Turn{Kind: session.ToolResult, ToolID: "t1", Text: strings.Join(lines, "\n")}
	got := renderTurn(5, turn, tailLimits(), 0)

	want := fmt.Sprintf("[#5 tool_result id=t1] %s\n… [20 lines truncated] …\n%s\n",
		strings.Join(lines[:40], "\n"), strings.Join(lines[60:], "\n"))
	if got != want {
		t.Errorf("tool result block mismatch:\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "line 50") {
		t.Error("middle lines must be gone")
	}
	if len(got) > 6100 {
		t.Errorf("tool result over 6000-char cap: %d", len(got))
	}
}

func TestRenderToolResultShortButOverCharCap(t *testing.T) {
	turn := session.Turn{Kind: session.ToolResult, Text: strings.Repeat("y", 100) + "\n" + strings.Repeat("z", 100)}
	got := renderTurn(1, turn, tailLimits(), 0)
	if strings.Contains(got, "lines truncated") {
		t.Error("few lines must not be line-truncated")
	}
	body := strings.SplitN(got, "] ", 2)[1]
	if len(body) > 6000 {
		t.Errorf("result body %d bytes over cap", len(body))
	}
}

func TestRenderToolInputCap(t *testing.T) {
	turn := session.Turn{Kind: session.ToolCall, ToolName: "Bash", ToolID: "t1", Input: strings.Repeat("c", 5000)}
	got := renderTurn(1, turn, tailLimits(), 0)
	if len(got) > len("[#1 tool_call Bash id=t1] ")+3000+1 {
		t.Errorf("tool input over 3000-char cap: %d", len(got))
	}
}

func TestRenderEditInputKeepsPath(t *testing.T) {
	input := `{"old_string":"` + strings.Repeat("a", 400) + `","file_path":"/tmp/proj/main.go"}`
	turn := session.Turn{Kind: session.ToolCall, ToolName: "Edit", Input: input}
	got := renderTurn(1, turn, olderLimits(), 0)
	if !strings.Contains(got, "path: /tmp/proj/main.go\n") {
		t.Errorf("path must be prepended when outside the first 300 chars: %q", got)
	}
	body := strings.SplitN(got, "] ", 2)[1]
	if len(body) > 600 {
		t.Errorf("edit input %d bytes over 600-char cap", len(body))
	}
	if !strings.HasPrefix(body, "path: /tmp/proj/main.go\n{\"old_string\":\"aaa") {
		t.Errorf("path must precede the first 300 input chars: %q", body)
	}
}

func TestRenderPatchInputPathAlreadyPresent(t *testing.T) {
	input := "*** Begin Patch\n*** Update File: main.go\n+" + strings.Repeat("x", 200)
	turn := session.Turn{Kind: session.ToolCall, ToolName: "apply_patch", Input: input}
	got := renderTurn(1, turn, olderLimits(), 0)
	if strings.Contains(got, "path: main.go") {
		t.Errorf("path already in the kept input must not be duplicated: %q", got)
	}
	if !strings.Contains(got, "*** Update File: main.go") {
		t.Errorf("patch header with the path must survive: %q", got)
	}
}

func TestRenderCompactionTailUncappedOlderCapped(t *testing.T) {
	text := strings.Repeat("s", 9000)
	turn := session.Turn{Kind: session.CompactionSummary, Text: text}
	tailGot := renderTurn(1, turn, tailLimits(), 0)
	if !strings.Contains(tailGot, text) {
		t.Error("tail compaction summary is uncapped")
	}
	olderGot := renderTurn(1, turn, olderLimits(), 0)
	body := strings.TrimSuffix(strings.SplitN(olderGot, "] ", 2)[1], "\n")
	if len(body) != 8000 {
		t.Errorf("older compaction summary capped at %d bytes, want 8000", len(body))
	}
}

func TestRenderAllKeepsLastReasoningTurns(t *testing.T) {
	turns := make([]session.Turn, 5)
	for i := range turns {
		turns[i] = session.Turn{Kind: session.Reasoning, Text: fmt.Sprintf("r%d", i+1)}
	}
	blocks := slices.DeleteFunc(renderAll(turns, tailLimits()), func(b string) bool { return b == "" })
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want the last 3 reasoning turns", len(blocks))
	}
	want := []string{"[#3 reasoning] r3\n", "[#4 reasoning] r4\n", "[#5 reasoning] r5\n"}
	for i, w := range want {
		if blocks[i] != w {
			t.Errorf("block %d = %q, want %q", i, blocks[i], w)
		}
	}
}

func TestOlderLimitsDropReasoning(t *testing.T) {
	turns := []session.Turn{
		{Kind: session.Reasoning, Text: "hidden"},
		{Kind: session.UserText, Text: "hi"},
	}
	blocks := slices.DeleteFunc(renderAll(turns, olderLimits()), func(b string) bool { return b == "" })
	if len(blocks) != 1 || !strings.Contains(blocks[0], "[#2 user] hi") {
		t.Errorf("older rendering must drop reasoning: %v", blocks)
	}
}
