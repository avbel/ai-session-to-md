//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"
)

const dirtyWarning = "Uncommitted changes are NOT in this file. Commit and push (e.g. a WIP branch) before switching machines."

// Scenario 1: claude-code --latest writes CLAUDE_CODE.md with the meta, the
// status, the last user message, the todos and the git warnings of the
// fixture.
func TestClaudeCodeLatest(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest")

	meta, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	mustEqual(t, meta.Harness, "claude-code", "meta harness")
	mustEqual(t, meta.SessionID, claudeSessionID, "meta session id")
	mustEqual(t, meta.Title, "Fix the failing test in pkg/reduce", "meta title")
	mustEqual(t, meta.Branch, "main", "meta branch")
	mustEqual(t, meta.Repo, fakeRemoteURL, "meta repo")
	if meta.Head == "" {
		t.Fatal("meta head is empty")
	}
	if meta.Raw {
		t.Fatal("meta raw = true in a summarized run")
	}
	mustEqual(t, meta.TLDR, "Fix the reduce test in pkg/reduce; stopped during a tool call; verify the test result first.", "meta tldr")

	mustEqual(t, statusLine(doc), "- **Status:** stopped during tool call `Bash`", "status")
	mustContain(t, doc, "go test ./pkg/reduce", "pending tool call")
	mustContain(t, doc, "  Fix the failing test in pkg/reduce", "last user message")
	mustContain(t, doc, "  - [ ] **in progress:** fix the failing test", "in-progress todo")
	mustContain(t, doc, "  - [ ] rerun go test", "pending todo")
	mustContain(t, doc, dirtyWarning, "dirty-tree warning")
	mustContain(t, doc, "branch main has no upstream; 1 commits exist only here", "unpushed warning")
	mustContain(t, doc, "- Ahead/behind upstream: no upstream", "upstream line")
	mustContain(t, doc, "- Files touched in session: `"+filepath.Join("pkg", "reduce", "reduce.go")+"`", "files touched")
	mustContain(t, doc, "TL;DR: Fix the reduce test in pkg/reduce", "summary TL;DR")

	reqs := llm.requireCount(t, 1)
	user := messageContent(t, reqs[0].Body, "user")
	mustContain(t, user, "Fix the failing test in pkg/reduce", "request transcript")
	mustContain(t, user, `"status":{"text":"stopped during tool call Bash"`, "request facts")
}

// Scenario 2a: codex --latest picks the user thread, not the newer subagent
// or spawned child rows.
func TestCodexLatest(t *testing.T) {
	e := newTestEnv(t)
	writeCodexStore(t, e)
	llm := e.useLLM(llmValid)

	res := e.runOK("codex", "--latest")

	// Drop diagnostics are --verbose-only (the default run stays quiet); the
	// re-run is --raw so it makes no extra LLM request.
	mustNotContain(t, res.Stderr, "drop codex thread", "quiet default run")
	resVerbose := e.runOK("codex", "--latest", "--verbose", "--raw", "--force")
	mustContain(t, resVerbose.Stderr, "drop codex thread "+codexSubID, "subagent drop log")
	mustContain(t, resVerbose.Stderr, "drop codex thread "+codexChildID, "child drop log")

	meta, doc := readHandoff(t, handoffPath(e, "CODEX.md"))
	mustEqual(t, meta.Harness, "codex", "meta harness")
	mustEqual(t, meta.SessionID, codexSessionID, "meta session id")
	mustEqual(t, meta.Title, "Codex E2E session", "meta title")
	mustEqual(t, statusLine(doc), "- **Status:** turn completed", "status")
	mustContain(t, doc, "  now run the tests", "last user message")
	mustContain(t, doc, "  - [x] write the codex fix", "completed todo")
	mustContain(t, doc, "  - [ ] **in progress:** run codex tests", "in-progress todo")
	mustContain(t, doc, dirtyWarning, "dirty-tree warning")

	reqs := llm.requireCount(t, 1)
	user := messageContent(t, reqs[0].Body, "user")
	mustContain(t, user, `"session_id":"`+codexSessionID+`"`, "request facts session id")
	mustNotContain(t, user, codexSubID, "request facts")
	mustNotContain(t, user, codexChildID, "request facts")
	mustNotContain(t, user, "SUBAGENT ONLY", "request body")
	mustNotContain(t, user, "CHILD ONLY", "request body")
	mustNotContain(t, user, "OTHER PROJECT", "request body")
}

// Scenario 2b: opencode --latest picks the top-level session in scope, not
// the newer child or the other project's session.
func TestOpenCodeLatest(t *testing.T) {
	e := newTestEnv(t)
	writeOpenCodeStore(t, e)
	llm := e.useLLM(llmValid)

	e.runOK("opencode", "--latest")

	meta, doc := readHandoff(t, handoffPath(e, "OPEN_CODE.md"))
	mustEqual(t, meta.Harness, "opencode", "meta harness")
	mustEqual(t, meta.SessionID, openCodeID, "meta session id")
	mustEqual(t, meta.Title, "OpenCode E2E session", "meta title")
	mustEqual(t, statusLine(doc), "- **Status:** turn completed", "status")
	mustContain(t, doc, "  Please write the opencode adapter", "last user message")
	mustContain(t, doc, "  - [x] write the opencode adapter", "completed todo")
	mustContain(t, doc, "  - [ ] **in progress:** run the opencode tests", "in-progress todo")
	mustContain(t, doc, dirtyWarning, "dirty-tree warning")

	reqs := llm.requireCount(t, 1)
	user := messageContent(t, reqs[0].Body, "user")
	mustContain(t, user, `"session_id":"`+openCodeID+`"`, "request facts session id")
	mustNotContain(t, user, openCodeChildID, "request facts")
	mustNotContain(t, user, "CHILD ONLY", "request body")
	mustNotContain(t, user, "SYNTHETIC ONLY", "request body")
	mustNotContain(t, user, "OTHER PROJECT", "request body")
}

// Scenario 3: three harnesses in one directory chain their handoffs; the third
// file's history has three ordered rows and the third request carries the
// previous Summary.
func TestChainClaudeCodexOpenCode(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	writeCodexStore(t, e)
	writeOpenCodeStore(t, e)
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest")
	waitNextSecond()
	e.runOK("codex", "--latest")

	codexMeta, codexDoc := readHandoff(t, handoffPath(e, "CODEX.md"))
	if got := len(codexMeta.History); got != 1 {
		t.Fatalf("CODEX.md meta history rows = %d, want 1", got)
	}
	if rows := historyRows(codexDoc); len(rows) != 2 {
		t.Fatalf("CODEX.md history table rows = %d, want 2\n%s", len(rows), codexDoc)
	}

	waitNextSecond()
	e.runOK("opencode", "--latest")

	meta, doc := readHandoff(t, handoffPath(e, "OPEN_CODE.md"))
	if got := len(meta.History); got != 2 {
		t.Fatalf("OPEN_CODE.md meta history rows = %d, want 2: %+v", got, meta.History)
	}
	if meta.History[0].Harness != "claude-code" || meta.History[1].Harness != "codex" {
		t.Fatalf("meta history order = %+v, want claude-code then codex", meta.History)
	}
	rows := historyRows(doc)
	if len(rows) != 3 {
		t.Fatalf("OPEN_CODE.md history table rows = %d, want 3\n%s", len(rows), doc)
	}
	mustContain(t, rows[0], claudeSessionID, "history row 1")
	mustContain(t, rows[1], codexSessionID, "history row 2")
	mustContain(t, rows[2], openCodeID, "history row 3")

	reqs := llm.requireCount(t, 3)
	prev := previousHandoff(t, messageContent(t, reqs[2].Body, "user"))
	mustContain(t, prev, "TL;DR: Fix the reduce test in pkg/reduce", "third request previous Summary")
	mustContain(t, prev, codexSessionID, "third request previous history")
	mustContain(t, prev, claudeSessionID, "third request inherited history")
}
