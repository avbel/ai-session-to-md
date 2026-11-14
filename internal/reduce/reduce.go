package reduce

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// Summarizer condenses one transcript chunk into notes. It is the seam that
// keeps this package independent of the LLM client; the caller owns the
// spinner and chunk numbering around each call.
type Summarizer func(ctx context.Context, chunk string) (string, error)

// Progress reports map-reduce progress: done is the 1-based index of the
// chunk about to be summarized and total is the chunk count of that phase.
type Progress func(done, total int, phase string)

// Reducer renders a session transcript and shrinks it to fit the LLM input
// budget (§11). The caller renders the <session_facts> block itself and only
// reports sizes: the budget subtracts SystemPromptLen, FactsLen and PrevLen
// as byte lengths. Raw runs steps 1–3 only — tail window, older-turn
// truncation and the tail budget check — and never calls Summarizer.
// OnChunk, when set, fires before each Summarizer call with phase "older"
// while condensing the older history into notes, and phase "notes" while
// re-summarizing notes that still overflow the budget.
type Reducer struct {
	MaxInputTokens  int
	SystemPromptLen int
	FactsLen        int
	PrevLen         int
	Summarizer      Summarizer
	OnChunk         Progress
	Raw             bool
}

const (
	tailWindowTurns = 40
	maxTailHalvings = 2
	maxNoteRounds   = 3
	notesHeader     = "[earlier history notes]\n\n"
)

var errTooLarge = errors.New("transcript too large for --max-input-tokens")

func (r Reducer) budget() int {
	return Budget(r.MaxInputTokens, r.SystemPromptLen, r.FactsLen, r.PrevLen)
}

// Reduce returns the reduced transcript, oldest part first. The tail window
// is checked first: when even the halved tail does not fit the budget, the
// error is returned before any Summarizer call. When the older part pushes
// the total over budget, it is map-reduced into notes.
func (r Reducer) Reduce(ctx context.Context, s session.Session) (string, error) {
	if len(s.Turns) == 0 {
		return "", nil
	}
	if !r.Raw && r.Summarizer == nil {
		return "", errors.New("reduce: Summarizer is required unless Raw")
	}
	l, err := r.fitTail(s.Turns)
	if err != nil {
		return "", err
	}
	start := len(s.Turns) - min(tailWindowTurns, len(s.Turns))
	olderText := strings.Join(renderAll(s.Turns[:start], olderLimits()), "")
	tailText := strings.Join(renderAll(s.Turns, l)[start:], "")
	if r.Raw {
		return olderText + tailText, nil
	}
	if EstimateTokens(olderText+tailText) <= r.budget() {
		return olderText + tailText, nil
	}
	notes, err := r.condenseOlder(ctx, s.Turns[:start], r.budget(), EstimateTokens(tailText))
	if err != nil {
		return "", err
	}
	if notes == "" {
		return tailText, nil
	}
	return notesHeader + notes + tailText, nil
}

// fitTail finds the tail limits whose rendering of the last tailWindowTurns
// turns fits the budget, halving the limits up to maxTailHalvings times.
// No Summarizer is involved.
func (r Reducer) fitTail(turns []session.Turn) (limits, error) {
	l := tailLimits()
	for i := range maxTailHalvings + 1 {
		start := len(turns) - min(tailWindowTurns, len(turns))
		blocks := renderAll(turns, l)[start:]
		if EstimateTokens(strings.Join(blocks, "")) <= r.budget() {
			return l, nil
		}
		if i < maxTailHalvings {
			l = l.halve()
		}
	}
	return l, fmt.Errorf("reduce: %w", errTooLarge)
}

// condenseOlder map-reduces the older turns into notes: chunks of at most
// budget/2 tokens are summarized sequentially, and the joined notes are
// re-summarized up to maxNoteRounds times until notes plus the tail fit.
func (r Reducer) condenseOlder(ctx context.Context, older []session.Turn, budget, tailTokens int) (string, error) {
	blocks := slices.DeleteFunc(renderAll(older, olderLimits()), func(b string) bool { return b == "" })
	chunkTokens := max(1, budget/2)
	notes, err := r.summarizeChunks(ctx, chunkBlocks(blocks, chunkTokens), "older")
	if err != nil {
		return "", fmt.Errorf("reduce: summarize older part: %w", err)
	}
	fits := func(s string) bool {
		return EstimateTokens(notesHeader+s)+tailTokens <= budget
	}
	if fits(notes) {
		return notes, nil
	}
	for range maxNoteRounds {
		notes, err = r.summarizeChunks(ctx, chunkBlocks([]string{notes}, chunkTokens), "notes")
		if err != nil {
			return "", fmt.Errorf("reduce: re-summarize notes: %w", err)
		}
		if fits(notes) {
			return notes, nil
		}
	}
	return "", fmt.Errorf("reduce: %w", errTooLarge)
}

// summarizeChunks calls the Summarizer once per chunk, sequentially, and
// joins the notes. phase is reported through OnChunk before each call.
func (r Reducer) summarizeChunks(ctx context.Context, chunks []string, phase string) (string, error) {
	notes := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if r.OnChunk != nil {
			r.OnChunk(i+1, len(chunks), phase)
		}
		note, err := r.Summarizer(ctx, chunk)
		if err != nil {
			return "", fmt.Errorf("reduce: summarize chunk %d/%d: %w", i+1, len(chunks), err)
		}
		notes = append(notes, note)
	}
	return strings.Join(notes, "\n\n"), nil
}

// chunkBlocks groups blocks into chunks of at most chunkTokens estimated
// tokens, splitting on turn boundaries and hard-splitting any single block
// larger than a chunk into pieces of chunk size.
func chunkBlocks(blocks []string, chunkTokens int) []string {
	chunks := make([]string, 0, len(blocks))
	var cur strings.Builder
	curTokens := 0
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur = strings.Builder{}
			curTokens = 0
		}
	}
	for _, b := range blocks {
		tokens := EstimateTokens(b)
		if tokens > chunkTokens {
			flush()
			chunks = append(chunks, hardSplit(b, chunkTokens)...)
			continue
		}
		if curTokens+tokens > chunkTokens {
			flush()
		}
		cur.WriteString(b)
		curTokens += tokens
	}
	flush()
	return chunks
}
