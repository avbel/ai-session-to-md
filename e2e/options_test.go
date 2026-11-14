//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/handoff"
)

// Scenario 6: --raw makes no HTTP request and writes the reduced transcript
// in place of the Summary.
func TestRawMakesNoHTTPRequests(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)

	res := e.runOK("claude-code", "--latest", "--raw")

	llm.requireCount(t, 0)
	mustNotContain(t, res.Stderr, "Summarizing", "raw stderr")

	meta, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	if !meta.Raw {
		t.Fatal("meta raw = false, want true")
	}
	mustEqual(t, meta.TLDR, meta.Title, "raw tldr")
	mustContain(t, doc, "## Transcript (raw, reduced)", "raw transcript heading")
	mustContain(t, doc, "[#1 user", "raw transcript body")
	mustContain(t, doc, "Fix the failing test in pkg/reduce", "raw transcript text")
	mustNotContain(t, doc, "## Summary", "raw document")
}

// Scenario 12: --prompt-file text and --prompt-append text are appended to
// the system prompt in that order.
func TestPromptExtensionOrder(t *testing.T) {
	const fileText = "FILE-MARKER: always list DB migrations separately"
	const appendText = "APPEND-MARKER: use repository-relative paths only"
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	promptFile := "extra-prompt.md"
	writeFile(t, handoffPath(e, promptFile), fileText)
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest", "--prompt-file", promptFile, "--prompt-append", appendText)

	reqs := llm.requireCount(t, 1)
	system := messageContent(t, reqs[0].Body, "system")
	wantSuffix := "\n\nAdditional instructions from the user:\n" + fileText +
		"\n\nAdditional instructions from the user:\n" + appendText
	if !strings.HasSuffix(system, wantSuffix) {
		t.Fatalf("system prompt does not end with the two appended blocks\n--- tail ---\n%s", tail(system, 400))
	}
	base, _, _ := strings.Cut(system, "\n\nAdditional instructions from the user:\n")
	mustEqual(t, base, handoff.SystemPrompt(nil), "system prompt base")
	if strings.Index(system, fileText) > strings.Index(system, appendText) {
		t.Fatal("prompt file text appears after --prompt-append text")
	}
}

// Scenario 13: --extra-body and --thinking reach the request body with the
// mapped fields.
func TestExtraBodyAndThinking(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest",
		"--extra-body", `{"temperature":0}`,
		"--thinking", "high",
		"--thinking-style", "openrouter")

	reqs := llm.requireCount(t, 1)
	body := reqs[0].Body
	model, _ := body["model"].(string)
	mustEqual(t, model, "test-model", "request model")
	if stream, ok := body["stream"].(bool); !ok || stream {
		t.Fatalf("request stream = %v, want false", body["stream"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("request body has no reasoning object: %v", body)
	}
	effort, _ := reasoning["effort"].(string)
	mustEqual(t, effort, "high", "reasoning effort")
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatal("openrouter style sent reasoning_effort")
	}
	temp, ok := body["temperature"].(float64)
	if !ok || temp != 0 {
		t.Fatalf("request temperature = %v, want 0", body["temperature"])
	}
}

// Scenario 14: -o - writes only the document to stdout; progress stays on
// stderr and no file appears in the working directory.
func TestStdoutOutputOnly(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	e.useLLM(llmValid)

	res := e.runOK("claude-code", "--latest", "-o", "-")

	if !strings.HasPrefix(res.Stdout, handoff.MetaPrefix) {
		t.Fatalf("stdout does not start with the meta comment:\n%s", head(res.Stdout, 300))
	}
	mustContain(t, res.Stdout, "# Handoff: Fix the failing test in pkg/reduce", "stdout document")
	mustContain(t, res.Stdout, "## Summary", "stdout document")
	mustNotContain(t, res.Stdout, "Reading session", "stdout")
	mustNotContain(t, res.Stdout, "wrote handoff", "stdout")
	mustNotContain(t, res.Stdout, "⚠", "stdout")
	mustContain(t, res.Stderr, "Reading session", "stderr progress")
	mustContain(t, res.Stderr, "wrote handoff to stdout", "stderr final line")

	if _, err := os.Stat(handoffPath(e, "CLAUDE_CODE.md")); err == nil {
		t.Fatal("-o - created CLAUDE_CODE.md")
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
