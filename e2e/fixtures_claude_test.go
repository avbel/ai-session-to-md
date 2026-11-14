//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	fakeRemoteURL = "https://github.com/example/e2e-fixture.git"

	claudeSessionID = "c1a0de01-aaaa-4b1b-8c2c-000000000001"
	claudeLargeID   = "c1a0de02-aaaa-4b1b-8c2c-000000000002"
	dupPrefix       = "dupsess"
	dupIDA          = dupPrefix + "-aaaaaa"
	dupIDB          = dupPrefix + "-bbbbbb"
	codexSessionID  = "c0de0001-bbbb-4c2c-9d3d-000000000002"
	codexSubID      = "c0de0002-bbbb-4c2c-9d3d-000000000003"
	codexChildID    = "c0de0003-bbbb-4c2c-9d3d-000000000004"
	codexOtherID    = "c0de0004-bbbb-4c2c-9d3d-000000000005"
	openCodeID      = "ses_e2e0c001"
	openCodeChildID = "ses_e2e0c002"
	openCodeOtherID = "ses_e2e0c003"

	claudeModel   = "claude-sonnet-4-5"
	claudeVersion = "2.0.0"
	codexModel    = "gpt-5.2"
	codexVersion  = "0.42.0"
)

// claudeSession builds a synthetic Claude Code JSONL transcript. Every entry
// is linked to the previous one through parentUuid so the adapter's active
// branch walk sees one linear chain.
type claudeSession struct {
	id     string
	cwd    string
	branch string
	lines  []map[string]any
	parent string
	uuid   int
	clock  time.Time
}

func newClaudeSession(id, cwd string) *claudeSession {
	return &claudeSession{
		id:     id,
		cwd:    cwd,
		branch: "main",
		clock:  time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC),
	}
}

func (c *claudeSession) next() string {
	c.uuid++
	c.clock = c.clock.Add(5 * time.Second)
	return fmt.Sprintf("uuid-%03d", c.uuid)
}

func (c *claudeSession) appendLine(typ string, message map[string]any) {
	uuid := c.next()
	line := map[string]any{
		"type":      typ,
		"uuid":      uuid,
		"timestamp": c.clock.Format(time.RFC3339),
		"cwd":       c.cwd,
		"gitBranch": c.branch,
		"version":   claudeVersion,
	}
	if c.parent != "" {
		line["parentUuid"] = c.parent
	}
	if message != nil {
		line["message"] = message
	}
	c.lines = append(c.lines, line)
	c.parent = uuid
}

func (c *claudeSession) userText(text string) *claudeSession {
	c.appendLine("user", map[string]any{
		"id": "msg-" + c.nextID(), "role": "user", "content": text,
	})
	return c
}

func (c *claudeSession) assistantText(text string) *claudeSession {
	c.appendLine("assistant", map[string]any{
		"id": "msg-" + c.nextID(), "role": "assistant", "model": claudeModel,
		"content": []any{map[string]any{"type": "text", "text": text}},
	})
	return c
}

func (c *claudeSession) assistantThinking(text string) *claudeSession {
	c.appendLine("assistant", map[string]any{
		"id": "msg-" + c.nextID(), "role": "assistant", "model": claudeModel,
		"content": []any{map[string]any{"type": "thinking", "thinking": text}},
	})
	return c
}

func (c *claudeSession) assistantTool(name, toolID string, input any) *claudeSession {
	c.appendLine("assistant", map[string]any{
		"id": "msg-" + c.nextID(), "role": "assistant", "model": claudeModel,
		"content": []any{map[string]any{
			"type": "tool_use", "id": toolID, "name": name, "input": input,
		}},
	})
	return c
}

func (c *claudeSession) todoWrite(todos ...map[string]any) *claudeSession {
	return c.assistantTool("TodoWrite", "toolu_todowrite", map[string]any{"todos": todos})
}

func (c *claudeSession) userToolResult(toolID, text string) *claudeSession {
	c.appendLine("user", map[string]any{
		"id": "msg-" + c.nextID(), "role": "user",
		"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": toolID, "content": text,
		}},
	})
	return c
}

// nextID returns a stable per-entry message id.
func (c *claudeSession) nextID() string {
	return fmt.Sprintf("%03d", c.uuid+1)
}

// write stores the transcript in its own project directory.
func (c *claudeSession) write(t *testing.T, claudeDir, project string) {
	t.Helper()
	var data []byte
	for _, line := range c.lines {
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal claude line: %v", err)
		}
		data = append(data, raw...)
		data = append(data, '\n')
	}
	writeFile(t, filepath.Join(claudeDir, "projects", project, c.id+".jsonl"), string(data))
}

// standardClaudeSession is the baseline fixture: a user request, a tool
// exchange, a todo list and a dangling Bash call.
func standardClaudeSession(e *testEnv) *claudeSession {
	c := newClaudeSession(claudeSessionID, e.repo)
	target := filepath.Join(e.repo, "pkg", "reduce", "reduce.go")
	c.userText("Fix the failing test in pkg/reduce")
	c.assistantText("On it.")
	c.assistantTool("Bash", "toolu_01", map[string]any{"command": "go test ./..."})
	c.userToolResult("toolu_01", "ok\nFAIL TestReduce")
	c.assistantThinking("the test asserts the old marker")
	c.assistantTool("Edit", "toolu_02", map[string]any{"file_path": target})
	c.userToolResult("toolu_02", "file edited")
	c.todoWrite(
		map[string]any{"content": "fix the failing test", "status": "in_progress"},
		map[string]any{"content": "rerun go test", "status": "pending"},
	)
	c.assistantTool("Bash", "toolu_03", map[string]any{"command": "go test ./pkg/reduce"})
	return c
}

// largeClaudeSession is a session long enough that its older turns must be
// map-reduced under a small --max-input-tokens.
func largeClaudeSession(e *testEnv) *claudeSession {
	c := newClaudeSession(claudeLargeID, e.repo)
	c.userText("Start the long refactor")
	for i := range 60 {
		c.assistantText(fmt.Sprintf("Step %d: %s", i, repeat("a", 1800)))
		c.userText(fmt.Sprintf("Continue %d: %s", i, repeat("b", 1800)))
	}
	c.assistantText("All steps done.")
	return c
}

// repeat keeps fixture bodies synthetic without literal long runs in source.
func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for range n {
		out = append(out, s[0])
	}
	return string(out)
}

// writeClaudeProject writes one session into the default project directory.
func writeClaudeProject(t *testing.T, e *testEnv, sessions ...*claudeSession) {
	t.Helper()
	if err := os.MkdirAll(e.claudeDir(), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	for _, s := range sessions {
		s.write(t, e.claudeDir(), "e2e-proj")
	}
}
