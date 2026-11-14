package reduce

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// fakeSummarizer records every call. reply builds the note for a chunk.
type fakeSummarizer struct {
	calls  int
	inputs []string
	reply  func(chunk string) string
	err    error
}

func (f *fakeSummarizer) summarize(ctx context.Context, chunk string) (string, error) {
	f.calls++
	f.inputs = append(f.inputs, chunk)
	if f.err != nil {
		return "", f.err
	}
	return f.reply(chunk), nil
}

func userTurns(n int, text string) []session.Turn {
	turns := make([]session.Turn, n)
	for i := range turns {
		turns[i] = session.Turn{Kind: session.UserText, Text: text}
	}
	return turns
}

func mustReduce(t *testing.T, r Reducer, s session.Session) string {
	t.Helper()
	out, err := r.Reduce(context.Background(), s)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	return out
}

func TestReduceTailOnlySessionNeedsNoLLM(t *testing.T) {
	s := session.Session{Turns: userTurns(30, strings.Repeat("x", 100))}
	fake := &fakeSummarizer{reply: func(string) string { return "never" }}
	out := mustReduce(t, Reducer{MaxInputTokens: 100000, Summarizer: fake.summarize}, s)
	if fake.calls != 0 {
		t.Errorf("summarizer called %d times for a fitting session", fake.calls)
	}
	want := ""
	for i := range 30 {
		want += fmt.Sprintf("[#%d user] %s\n", i+1, strings.Repeat("x", 100))
	}
	if out != want {
		t.Errorf("tail-only output mismatch:\n%q", out[:200])
	}
}

func TestReduceOlderAndTailFitNeedsNoLLM(t *testing.T) {
	s := session.Session{Turns: append(userTurns(10, strings.Repeat("o", 100)), userTurns(40, strings.Repeat("t", 100))...)}
	fake := &fakeSummarizer{reply: func(string) string { return "never" }}
	out := mustReduce(t, Reducer{MaxInputTokens: 100000, Summarizer: fake.summarize}, s)
	if fake.calls != 0 {
		t.Errorf("summarizer called %d times", fake.calls)
	}
	if !strings.HasPrefix(out, "[#1 user] ooo") {
		t.Errorf("older part missing: %q", out[:40])
	}
	if !strings.Contains(out, "[#50 user] "+strings.Repeat("t", 100)) {
		t.Error("tail numbering must continue the session's turn numbers")
	}
}

func TestReduceTailHalving(t *testing.T) {
	s := session.Session{Turns: userTurns(40, strings.Repeat("x", 20000))}
	fake := &fakeSummarizer{reply: func(string) string { return "never" }}
	out := mustReduce(t, Reducer{MaxInputTokens: 54120, Summarizer: fake.summarize}, s)
	if fake.calls != 0 {
		t.Errorf("summarizer called %d times during tail halving", fake.calls)
	}
	if n := strings.Count(out, textCutMarker); n != 40 {
		t.Errorf("got %d cut markers, want 40 (one per halved turn)", n)
	}
	// After two halvings each text is capped at 5000 bytes: 40 blocks of
	// 11+5000+1 bytes minus the 31 two-digit headers counted once (431
	// header bytes total).
	if len(out) != 431+40*5001 {
		t.Errorf("output length %d, want %d", len(out), 431+40*5001)
	}
	if strings.Contains(out, strings.Repeat("x", 3000)) {
		t.Error("no run longer than the 2492-byte head/tail halves may remain")
	}
}

func TestReduceTailTooLargeNoLLM(t *testing.T) {
	s := session.Session{Turns: userTurns(40, strings.Repeat("x", 20000))}
	fake := &fakeSummarizer{reply: func(string) string { return "never" }}
	_, err := Reducer{MaxInputTokens: 4000, Summarizer: fake.summarize}.Reduce(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "transcript too large for --max-input-tokens") {
		t.Fatalf("err = %v, want the too-large error", err)
	}
	if fake.calls != 0 {
		t.Errorf("summarizer called %d times before the too-large error", fake.calls)
	}
}

func TestReduceMapReduceReplacesOlderPart(t *testing.T) {
	s := session.Session{Turns: append(userTurns(60, strings.Repeat("o", 4000)), userTurns(40, strings.Repeat("t", 4000))...)}
	fake := &fakeSummarizer{reply: func(string) string { return "note" }}
	out := mustReduce(t, Reducer{MaxInputTokens: 64000, Summarizer: fake.summarize}, s)

	if fake.calls != 3 {
		t.Errorf("map rounds: %d summarizer calls, want 3 chunks", fake.calls)
	}
	for i, chunk := range fake.inputs {
		if !strings.HasPrefix(chunk, "[#") || !strings.HasSuffix(chunk, "\n") {
			t.Errorf("chunk %d is not split on turn boundaries: %q…%q", i, chunk[:20], chunk[len(chunk)-20:])
		}
		if got := EstimateTokens(chunk); got > 30000 {
			t.Errorf("chunk %d is %d tokens, over the budget/2 cap", i, got)
		}
	}
	if !strings.HasPrefix(out, notesHeader) {
		t.Errorf("output must start with %q", notesHeader)
	}
	if strings.Contains(out, "oooo") {
		t.Error("older part must be replaced by the notes")
	}
	if strings.Count(strings.TrimPrefix(out, notesHeader), "note") != 3 {
		t.Error("want the 3 joined notes in the output")
	}
	if !strings.Contains(out, strings.Repeat("t", 4000)) {
		t.Error("tail must be kept almost verbatim")
	}
	if !strings.HasSuffix(out, "[#100 user] "+strings.Repeat("t", 4000)+"\n") {
		t.Error("output must end with the last tail block")
	}
}

func TestReduceResummarizesNotesOneRound(t *testing.T) {
	s := session.Session{Turns: append(userTurns(60, strings.Repeat("o", 4000)), userTurns(40, strings.Repeat("t", 1000))...)}
	fake := &fakeSummarizer{reply: func(chunk string) string {
		if strings.Contains(chunk, "LLLL") {
			return "short"
		}
		return strings.Repeat("L", 50000)
	}}
	out := mustReduce(t, Reducer{MaxInputTokens: 34000, Summarizer: fake.summarize}, s)

	// 5 map chunks of the older part, then 5 hard-split pieces of the too
	// long notes; the second round's short notes fit the budget.
	if fake.calls != 10 {
		t.Errorf("summarizer calls = %d, want 5 map + 5 re-summarize", fake.calls)
	}
	if n := strings.Count(out, "short"); n != 5 {
		t.Errorf("got %d short notes, want 5", n)
	}
	if strings.Contains(out, "LLLL") || strings.Contains(out, "oooo") {
		t.Error("re-summarized notes must replace both the older part and the long notes")
	}
	if !strings.HasPrefix(out, notesHeader) {
		t.Errorf("output must start with %q", notesHeader)
	}
}

func TestReduceTooLargeAfterThreeNoteRounds(t *testing.T) {
	s := session.Session{Turns: append(userTurns(60, strings.Repeat("o", 4000)), userTurns(40, strings.Repeat("t", 1000))...)}
	fake := &fakeSummarizer{reply: func(string) string { return strings.Repeat("L", 50000) }}
	_, err := Reducer{MaxInputTokens: 34000, Summarizer: fake.summarize}.Reduce(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "transcript too large for --max-input-tokens") {
		t.Fatalf("err = %v, want the too-large error after the note rounds", err)
	}
	// 5 map chunks, then 3 re-summarize rounds over the fixed-size notes,
	// each re-split into 5 chunks: 5 + 3*5 = 20 calls, then the error.
	if fake.calls != 20 {
		t.Errorf("summarizer calls = %d, want 20 (map + 3 capped rounds)", fake.calls)
	}
}

func TestReduceRawSkipsMapReduce(t *testing.T) {
	s := session.Session{Turns: append(userTurns(60, strings.Repeat("o", 4000)), userTurns(40, strings.Repeat("t", 1000))...)}
	out := mustReduce(t, Reducer{MaxInputTokens: 34000, Raw: true}, s)
	if strings.Contains(out, "earlier history notes") {
		t.Error("raw mode must not map-reduce")
	}
	if !strings.Contains(out, "oooo") {
		t.Error("raw mode keeps the truncated older part")
	}
	if !strings.Contains(out, "[#100 user] "+strings.Repeat("t", 1000)) {
		t.Error("raw mode keeps the tail")
	}
}

func TestReduceRawTailStillBudgetChecked(t *testing.T) {
	s := session.Session{Turns: userTurns(40, strings.Repeat("x", 20000))}
	_, err := Reducer{MaxInputTokens: 4000, Raw: true}.Reduce(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "transcript too large for --max-input-tokens") {
		t.Fatalf("err = %v, want the too-large error in raw mode too", err)
	}
}

func TestReduceRequiresSummarizerUnlessRaw(t *testing.T) {
	s := session.Session{Turns: userTurns(5, "hi")}
	_, err := Reducer{MaxInputTokens: 100000}.Reduce(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "Summarizer") {
		t.Fatalf("err = %v, want a Summarizer-required error", err)
	}
}

func TestReduceEmptySession(t *testing.T) {
	out, err := Reducer{MaxInputTokens: 100000}.Reduce(context.Background(), session.Session{})
	if err != nil || out != "" {
		t.Errorf("empty session: out = %q, err = %v", out, err)
	}
}

func TestReduceSummarizerErrorWrapped(t *testing.T) {
	s := session.Session{Turns: append(userTurns(60, strings.Repeat("o", 4000)), userTurns(40, strings.Repeat("t", 1000))...)}
	fake := &fakeSummarizer{err: errors.New("boom")}
	_, err := Reducer{MaxInputTokens: 34000, Summarizer: fake.summarize}.Reduce(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "chunk 1/") {
		t.Fatalf("err = %v, want the wrapped summarizer error", err)
	}
}
