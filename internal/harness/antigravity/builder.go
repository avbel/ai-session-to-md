package antigravity

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

const noResultText = "no result recorded"

// fileTools are the agy tools whose TargetFile argument is a file they write.
var fileTools = map[string]struct{}{
	"write_to_file":              {},
	"replace_file_content":       {},
	"multi_replace_file_content": {},
}

// builder turns transcript records into turns. Tool results have no call id,
// so each planner step's calls queue up and the following result records
// close them in order.
type builder struct {
	brainDir     string
	turns        []session.Turn
	pending      []session.Turn
	files        []string
	seen         map[string]struct{}
	model        string
	endedOnError bool
}

func newBuilder(brainDir string) *builder {
	return &builder{brainDir: brainDir, seen: map[string]struct{}{}}
}

func (b *builder) addAll(recs []record) {
	for i, r := range recs {
		b.add(r, i == len(recs)-1)
	}
	b.endedOnError = endsOnError(recs)
}

func (b *builder) add(r record, last bool) {
	at := parseTime(r.CreatedAt)
	switch {
	case r.Type == typeUserInput:
		b.closePending(at)
		if t := userText(r.Content); t != "" {
			b.turns = append(b.turns, session.Turn{Kind: session.UserText, Time: at, Text: t})
		}
	case r.Type == typePlanner:
		b.planner(r, at)
	case r.Type == typeCheckpoint:
		if t := strings.TrimSpace(r.Content); t != "" {
			b.turns = append(b.turns, session.Turn{Kind: session.CompactionSummary, Time: at, Text: t})
		}
	case r.isResult():
		// A command still RUNNING in the last record is the call the
		// session stopped in; earlier RUNNING results are async commands
		// agy moved on from.
		if r.Status == statusRunning && last {
			return
		}
		b.result(r, at)
	}
}

func (b *builder) planner(r record, at time.Time) {
	b.closePending(at)
	if r.Model != "" {
		b.model = r.Model
	}
	if t := strings.TrimSpace(r.Thinking); t != "" {
		b.turns = append(b.turns, session.Turn{Kind: session.Reasoning, Time: at, Text: t})
	}
	if t := strings.TrimSpace(r.Content); t != "" {
		b.turns = append(b.turns, session.Turn{Kind: session.AssistantText, Time: at, Text: t})
	}
	for i, tc := range r.ToolCalls {
		call := session.Turn{
			Kind:     session.ToolCall,
			Time:     at,
			ToolName: tc.Name,
			ToolID:   strconv.Itoa(r.StepIndex) + "-" + strconv.Itoa(i),
			Input:    callInput(tc),
		}
		b.turns = append(b.turns, call)
		b.pending = append(b.pending, call)
		b.noteFile(tc)
	}
}

func (b *builder) result(r record, at time.Time) {
	t := session.Turn{
		Kind:    session.ToolResult,
		Time:    at,
		Text:    resultText(r.Content),
		IsError: r.Status == statusError,
	}
	if t.Text == "" {
		t.Text = strings.TrimSpace(r.Error)
	}
	if len(b.pending) > 0 {
		t.ToolID, t.ToolName = b.pending[0].ToolID, b.pending[0].ToolName
		b.pending = b.pending[1:]
	} else {
		t.ToolName = strings.ToLower(r.Type)
	}
	b.turns = append(b.turns, t)
}

// closePending ends the calls a new planner step or user input left behind:
// agy dropped them (a denied or failed sibling call stops the batch), so they
// must not read as the call the session stopped in.
func (b *builder) closePending(at time.Time) {
	for _, call := range b.pending {
		b.turns = append(b.turns, session.Turn{
			Kind: session.ToolResult, Time: at, ToolID: call.ToolID, ToolName: call.ToolName,
			Text: noResultText, IsError: true,
		})
	}
	b.pending = nil
}

// noteFile records the target of a file-writing call, skipping agy's own
// artifacts (task.md, implementation_plan.md) kept in the brain dir.
func (b *builder) noteFile(tc toolCall) {
	if _, ok := fileTools[tc.Name]; !ok {
		return
	}
	var isArtifact bool
	if raw, ok := tc.Args["IsArtifact"]; ok {
		_ = json.Unmarshal(raw, &isArtifact)
	}
	path := strings.TrimSpace(argString(tc, "TargetFile"))
	if path == "" || isArtifact || inDir(path, b.brainDir) {
		return
	}
	if _, ok := b.seen[path]; ok {
		return
	}
	b.seen[path] = struct{}{}
	b.files = append(b.files, path)
}

func inDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// endsOnError reports that the conversation's last step after the final
// user input is a model error (e.g. an exhausted 503 retry), not an answer.
func endsOnError(recs []record) bool {
	for _, rec := range slices.Backward(recs) {
		switch {
		case rec.Type == typeError:
			return true
		case rec.Type == typePlanner, rec.Type == typeUserInput, rec.isResult():
			return false
		}
	}
	return false
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
