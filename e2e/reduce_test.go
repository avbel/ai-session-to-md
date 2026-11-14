//go:build e2e

package e2e

import (
	"strconv"
	"testing"
)

// Scenario 9: a small --max-input-tokens forces the older turns through the
// map step; the final request carries the earlier history notes.
func TestMapReduceChunking(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, largeClaudeSession(e))
	llm := e.useLLM(llmValid)

	e.runOK("claude-code", "--latest", "--max-input-tokens", "30000")

	reqs := llm.all()
	if len(reqs) < 2 {
		t.Fatalf("LLM requests = %d, want more than one (chunk + summary)", len(reqs))
	}
	for i, req := range reqs[:len(reqs)-1] {
		system := messageContent(t, req.Body, "system")
		mustContain(t, system, "You condense one portion", "chunk request "+strconv.Itoa(i))
	}
	final := reqs[len(reqs)-1]
	mustContain(t, messageContent(t, final.Body, "system"), `You write the "Summary" section`, "final request system")
	user := messageContent(t, final.Body, "user")
	mustContain(t, user, "[earlier history notes]", "final request transcript")

	meta, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	mustEqual(t, meta.TLDR, "Fix the reduce test in pkg/reduce; stopped during a tool call; verify the test result first.", "meta tldr")
	mustContain(t, doc, "## Summary", "handoff")
}

// Scenario 10: an invalid first Summary triggers exactly one re-roll; the
// second answer is accepted, the file is written and the warning is printed.
func TestRerollOnBrokenSummary(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmBrokenFirst)

	res := e.runOK("claude-code", "--latest")

	reqs := llm.requireCount(t, 2)
	mustContain(t, res.Stderr, "re-rolled", "stderr warning")
	mustContain(t, messageContent(t, reqs[1].Body, "user"), "Your previous answer is missing these required parts:", "re-roll request")

	meta, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	mustEqual(t, meta.TLDR, "Fix the reduce test in pkg/reduce; stopped during a tool call; verify the test result first.", "meta tldr")
	mustContain(t, doc, "### Key files, commands and references", "handoff summary")
	mustNotContain(t, doc, "Here is a summary.", "handoff summary")
}
