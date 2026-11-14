//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// llmMode selects the fake server's behaviour for one run.
type llmMode int

const (
	llmValid       llmMode = iota // always a valid Summary
	llmBrokenFirst                // an invalid Summary once, then valid
	llm429Once                    // 429 with Retry-After once, then valid
	llm401                        // 401 forever
	llmSlow                       // sleeps past a short --timeout and then answers
)

const slowDelay = 2 * time.Second

// validSummary is the canned answer the validator accepts: one short TL;DR,
// the eight headings in order and a numbered Next steps body.
const validSummary = `TL;DR: Fix the reduce test in pkg/reduce; stopped during a tool call; verify the test result first.
### Goal
Fix the failing test in pkg/reduce so the package passes again.
### Current state
The session stopped during a tool call. The working tree has uncommitted changes.
### Done so far
- Reproduced the failure with ` + "`go test ./pkg/reduce`" + `.
- Edited pkg/reduce/reduce.go.
### Decisions and rationale
- Keep the existing truncation marker because other tests assert it.
### Tried and failed
- none
### Next steps
1. Verify the pending command finished and inspect its output.
2. Re-run ` + "`go test ./pkg/reduce`" + ` and then the full suite.
### Open questions and blockers
none
### Key files, commands and references
- ` + "`pkg/reduce/reduce.go`" + `
- ` + "`go test ./pkg/reduce`" + `
`

// brokenSummary fails the heading, TL;DR-order and Next steps checks.
const brokenSummary = `Here is a summary.
TL;DR: Short and incomplete.
### Goal
Only one of the required sections is present.
`

// recordedRequest is one captured chat/completions request.
type recordedRequest struct {
	Raw  string
	Body map[string]any
}

// fakeLLM is an OpenAI-compatible server that records every request body.
type fakeLLM struct {
	server *httptest.Server
	mode   llmMode

	mu       sync.Mutex
	requests []recordedRequest
}

func newFakeLLM(t *testing.T, mode llmMode) *fakeLLM {
	t.Helper()
	f := &fakeLLM{mode: mode}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLLM) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{Raw: string(raw), Body: body})
	n := len(f.requests)
	f.mu.Unlock()

	switch f.mode {
	case llmBrokenFirst:
		if n == 1 {
			writeChatResponse(w, brokenSummary)
			return
		}
	case llm429Once:
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":"rate limited"}`)
			return
		}
	case llm401:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
		return
	case llmSlow:
		select {
		case <-time.After(slowDelay):
		case <-r.Context().Done():
			return
		}
	case llmValid:
	}
	writeChatResponse(w, validSummary)
}

// writeChatResponse answers with a single non-streamed choice.
func writeChatResponse(w http.ResponseWriter, content string) {
	resp := map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 60},
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (f *fakeLLM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeLLM) all() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

// requireCount fails unless the server saw exactly n requests.
func (f *fakeLLM) requireCount(t *testing.T, n int) []recordedRequest {
	t.Helper()
	reqs := f.all()
	if len(reqs) != n {
		t.Fatalf("LLM requests = %d, want %d\nbodies:\n%s", len(reqs), n, dumpBodies(reqs))
	}
	return reqs
}

func dumpBodies(reqs []recordedRequest) string {
	var b strings.Builder
	for i, r := range reqs {
		fmt.Fprintf(&b, "--- request %d ---\n%s\n", i+1, r.Raw)
	}
	return b.String()
}

// messageContent returns the content of the first message with that role.
func messageContent(t *testing.T, body map[string]any, role string) string {
	t.Helper()
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("request body has no messages: %v", body)
	}
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if msg["role"] == role {
			content, _ := msg["content"].(string)
			return content
		}
	}
	t.Fatalf("request body has no %q message", role)
	return ""
}

// previousHandoff extracts the <previous_handoff> block from a user message.
func previousHandoff(t *testing.T, user string) string {
	t.Helper()
	const open, close = "<previous_handoff>\n", "\n</previous_handoff>"
	_, rest, ok := strings.Cut(user, open)
	if !ok {
		t.Fatalf("user message has no <previous_handoff> block:\n%s", user)
	}
	block, _, ok := strings.Cut(rest, close)
	if !ok {
		t.Fatalf("user message has no closing <previous_handoff> tag")
	}
	return block
}
