//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"
)

// The redaction-fixture credentials, built by concatenation so no scanner
// flags the source.
var (
	redactFixtureToken  = "gh" + "p_" + "e2e" + "token" + repeat("z", 28)
	redactFixtureSecret = "sk" + "-" + repeat("y", 24)
	redactFixtureRemote = "https://ci-bot:" + redactFixtureToken + "@github.com/example/redact-fixture.git"
)

// Git-redaction: a remote URL with token-only userinfo and a commit subject
// carrying a fake key must reach neither the handoff file nor the LLM
// request; the redaction markers do.
func TestGitRedaction(t *testing.T) {
	e := newTestEnv(t)
	gitRun(t, e.repo, "remote", "set-url", "origin", redactFixtureRemote)
	writeFile(t, filepath.Join(e.repo, "client.go"), "package main\n")
	gitRun(t, e.repo, "add", "client.go")
	gitRun(t, e.repo, "commit", "-m", "wire up client "+redactFixtureSecret)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest")

	meta, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	reqs := llm.requireCount(t, 1)
	user := messageContent(t, reqs[0].Body, "user")

	for _, s := range []string{redactFixtureToken, redactFixtureSecret} {
		mustNotContain(t, doc, s, "handoff file")
		mustNotContain(t, user, s, "request body")
	}
	mustNotContain(t, meta.Title, redactFixtureSecret, "meta title")
	mustContain(t, doc, "[REDACTED:", "handoff redaction marker")
	// The credential userinfo is stripped or marked, but the host stays.
	mustContain(t, doc, "github.com/example/redact-fixture.git", "handoff repo host")
}

// The git facts come from the session's own cwd, not from the process cwd:
// the fixture session ran in a nested git repository inside the scope root.
func TestGitFactsFromSessionCwd(t *testing.T) {
	e := newTestEnv(t)
	sub := filepath.Join(e.repo, "vendor", "nested")
	writeFile(t, filepath.Join(sub, "keep.txt"), "placeholder")
	gitRun(t, sub, "init", "-b", "feature-b")
	gitRun(t, sub, "config", "user.name", "E2E Bot")
	gitRun(t, sub, "config", "user.email", "e2e@example.com")
	writeFile(t, filepath.Join(sub, "nested.go"), "package nested\n")
	gitRun(t, sub, "add", ".")
	gitRun(t, sub, "commit", "-m", "work in the nested repo")
	gitRun(t, sub, "remote", "add", "origin", "https://github.com/example/nested-fixture.git")
	writeFile(t, filepath.Join(sub, "nested.go"), "package nested\n\nvar dirty = true\n")

	c := newClaudeSession(claudeSessionID, sub)
	c.userText("Work in the nested repository")
	c.assistantText("On it.")
	c.assistantTool("Bash", "toolu_nested", map[string]any{"command": "go test ./..."})
	writeClaudeProject(t, e, c)
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest")

	_, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	reqs := llm.requireCount(t, 1)
	user := messageContent(t, reqs[0].Body, "user")

	mustContain(t, doc, "github.com/example/nested-fixture.git", "session-cwd remote")
	mustContain(t, doc, "feature-b", "session-cwd branch")
	mustContain(t, doc, dirtyWarning, "session-cwd dirty state")
	mustNotContain(t, doc, fakeRemoteURL, "process-cwd remote must not leak into the git facts")
	// The session recorded branch "main" (the fixture's gitBranch), so the
	// branch-mismatch warning fires against the live "feature-b".
	mustContain(t, doc, `Current branch is "feature-b"; the session was recorded on "main".`, "branch mismatch warning")
	mustContain(t, user, `"repo_url":"https://github.com/example/nested-fixture.git"`, "request facts remote")
}
