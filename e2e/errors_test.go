//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/handoff"
)

// Scenario 7: an existing file needs --force without a TTY, and a symlinked
// output target is always refused.
func TestOverwriteRules(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)
	e.runOK("claude-code", "--latest")

	assertNoForceRefusal(t, e)
	assertForceOverwrites(t, e)
	assertSymlinkRefused(t, e)
	llm.requireCount(t, 2)
}

// assertNoForceRefusal requires exit 2 and an untouched file.
func assertNoForceRefusal(t *testing.T, e *testEnv) {
	t.Helper()
	path := handoffPath(e, "CLAUDE_CODE.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first handoff: %v", err)
	}
	res := e.run("claude-code", "--latest")
	wantExit(t, res, 2)
	mustContain(t, res.Stderr, "pass --force", "stderr")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read handoff after refusal: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("handoff changed after a refused overwrite")
	}
}

// assertForceOverwrites replaces the file content and requires --force to
// rewrite it.
func assertForceOverwrites(t *testing.T, e *testEnv) {
	t.Helper()
	path := handoffPath(e, "CLAUDE_CODE.md")
	writeFile(t, path, "SENTINEL\n")
	e.runOK("claude-code", "--latest", "--force")
	over, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read forced handoff: %v", err)
	}
	if string(over) == "SENTINEL\n" {
		t.Fatal("--force did not overwrite the file")
	}
	if !bytes.HasPrefix(over, []byte(handoff.MetaPrefix)) {
		t.Fatalf("forced handoff does not start with the meta comment:\n%s", head(string(over), 200))
	}
}

// assertSymlinkRefused requires exit 2 for a symlinked -o target and no change
// to the link target.
func assertSymlinkRefused(t *testing.T, e *testEnv) {
	t.Helper()
	target := filepath.Join(e.repo, "symlink-target.md")
	writeFile(t, target, "target\n")
	if err := os.Symlink(target, filepath.Join(e.repo, "symlink-out.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res := e.run("claude-code", "--latest", "-o", "symlink-out.md", "--force")
	wantExit(t, res, 2)
	mustContain(t, res.Stderr, "symlink", "stderr")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	mustEqual(t, string(data), "target\n", "symlink target content")
}

// Scenario 8: the documented exit codes for the failure paths.
func TestExitCodes(t *testing.T) {
	t.Run("no sessions", exitNoSessions)
	t.Run("ambiguous id prefix", exitAmbiguousPrefix)
	t.Run("missing model", exitMissingModel)
	t.Run("non-loopback http base url", exitBadBaseURL)
	t.Run("llm 401 is final", exitLLM401)
	t.Run("429 then 200", exitRetry429)
	t.Run("slow response times out", exitSlowTimeout)
}

func exitNoSessions(t *testing.T) {
	e := newTestEnv(t)
	res := e.run("claude-code", "--latest")
	wantExit(t, res, 3)
	mustContain(t, res.Stderr, "no sessions", "stderr")
}

func exitAmbiguousPrefix(t *testing.T) {
	e := newTestEnv(t)
	first := newClaudeSession(dupIDA, e.repo)
	first.userText("first duplicate session")
	first.assistantText("one")
	second := newClaudeSession(dupIDB, e.repo)
	second.userText("second duplicate session")
	second.assistantText("two")
	writeClaudeProject(t, e, first, second)
	llm := e.useLLM(llmValid)

	res := e.run("claude-code", dupPrefix)
	wantExit(t, res, 2)
	mustContain(t, res.Stderr, dupIDA, "stderr matches")
	mustContain(t, res.Stderr, dupIDB, "stderr matches")
	llm.requireCount(t, 0)
}

func exitMissingModel(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)
	e.unset("AISMD_MODEL")

	res := e.run("claude-code", "--latest")
	wantExit(t, res, 2)
	mustContain(t, res.Stderr, "model", "stderr")
	llm.requireCount(t, 0)
}

func exitBadBaseURL(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)
	e.set("AISMD_BASE_URL", "http://example.com/v1")

	res := e.run("claude-code", "--latest")
	wantExit(t, res, 2)
	mustContain(t, res.Stderr, "cleartext", "stderr")
	llm.requireCount(t, 0)
}

func exitLLM401(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llm401)

	res := e.run("claude-code", "--latest")
	wantExit(t, res, 4)
	mustContain(t, res.Stderr, "401", "stderr")
	mustNotContain(t, res.Stderr, "test-key-fake", "stderr")
	mustNotContain(t, res.Stderr, "Authorization", "stderr")
	llm.requireCount(t, 1)
}

func exitRetry429(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llm429Once)

	e.runOK("claude-code", "--latest")
	llm.requireCount(t, 2)
}

func exitSlowTimeout(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmSlow)

	res := e.run("claude-code", "--latest", "--timeout", "500ms")
	wantExit(t, res, 4)
	llm.requireCount(t, 1)
}
