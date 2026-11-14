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
// re-roll continues the conversation (system, original user message, first
// answer, re-roll request), the second answer is accepted and the file is
// written. The re-roll note itself is --verbose only.
func TestRerollOnBrokenSummary(t *testing.T) {
	e := newTestEnv(t)
	writeClaudeProject(t, e, standardClaudeSession(e))
	llm := e.useLLM(llmBrokenFirst)

	res := e.runOK("claude-code", "--latest")
	mustNotContain(t, res.Stderr, "re-rolled", "re-roll note is --verbose only")

	reqs := llm.requireCount(t, 2)
	msgs := allMessages(t, reqs[1].Body)
	if len(msgs) != 4 {
		t.Fatalf("re-roll request has %d messages, want 4: %+v", len(msgs), rolesOf(msgs))
	}
	wantRoles := []string{"system", "user", "assistant", "user"}
	for i, role := range wantRoles {
		if msgs[i].Role != role {
			t.Errorf("re-roll message %d role = %q, want %q", i+1, msgs[i].Role, role)
		}
	}
	original := allMessages(t, reqs[0].Body)
	if len(original) != 2 {
		t.Fatalf("first request has %d messages, want 2", len(original))
	}
	mustEqual(t, msgs[1].Content, original[1].Content, "re-roll keeps the original user message")
	mustEqual(t, msgs[2].Content, brokenSummary, "re-roll carries the first answer")
	mustContain(t, msgs[3].Content, "Your previous answer is missing these required parts:", "re-roll request")

	meta, doc := readHandoff(t, handoffPath(e, "CLAUDE_CODE.md"))
	mustEqual(t, meta.TLDR, "Fix the reduce test in pkg/reduce; stopped during a tool call; verify the test result first.", "meta tldr")
	mustContain(t, doc, "### Key files, commands and references", "handoff summary")
	mustNotContain(t, doc, "Here is a summary.", "handoff summary")

	// With --verbose the re-roll note is logged.
	resVerbose := e.runOK("claude-code", "--latest", "--verbose", "--force")
	mustContain(t, resVerbose.Stderr, "summary was re-rolled once", "verbose stderr")
	mustContain(t, resVerbose.Stderr, "re-roll: missing", "verbose re-roll reason")
	llm.requireCount(t, 4)
}

func rolesOf(msgs []chatMsg) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}
