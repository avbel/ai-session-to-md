package antigravity

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/jsonl"
)

// Step types and statuses of the transcript records (agy 1.5.16).
const (
	typeUserInput   = "USER_INPUT"
	typePlanner     = "PLANNER_RESPONSE"
	typeCheckpoint  = "CHECKPOINT"
	typeError       = "ERROR_MESSAGE"
	sourceModel     = "MODEL"
	statusError     = "ERROR"
	statusRunning   = "RUNNING"
	toolAction      = "toolAction"
	toolSummary     = "toolSummary"
	userRequestOpen = "<USER_REQUEST>"
)

// record is one transcript line. Tool results carry no call id: they are the
// MODEL-source records that follow a PLANNER_RESPONSE, one per tool call, in
// call order.
type record struct {
	StepIndex int        `json:"step_index"`
	Source    string     `json:"source"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	CreatedAt string     `json:"created_at"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking"`
	Error     string     `json:"error"`
	Model     string     `json:"model"`
	ToolCalls []toolCall `json:"tool_calls"`
}

type toolCall struct {
	Name string                     `json:"name"`
	Args map[string]json.RawMessage `json:"args"`
}

// isResult reports a tool-result record: any MODEL step except the planner's
// own responses (VIEW_FILE, RUN_COMMAND, CODE_ACTION, GENERIC, ...).
func (r record) isResult() bool {
	return r.Source == sourceModel && r.Type != typePlanner
}

// readRecords parses the transcript; malformed and typeless lines are
// counted in skipped, never fatal.
func readRecords(ctx context.Context, path string) ([]record, int, error) {
	seq, closeFn, err := jsonl.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open antigravity transcript %s: %w", path, err)
	}
	defer closeFn()
	var recs []record
	skipped := 0
	for _, raw := range seq {
		if err := ctx.Err(); err != nil {
			return nil, skipped, fmt.Errorf("load antigravity transcript: %w", err)
		}
		var r record
		if json.Unmarshal(raw, &r) != nil || r.Type == "" {
			skipped++
			continue
		}
		r.ToolCalls = unwrapCalls(r.ToolCalls)
		recs = append(recs, r)
	}
	// agy can write a planner step after the results of its own calls;
	// step_index is the conversation order the pairing relies on.
	slices.SortStableFunc(recs, func(x, y record) int { return cmp.Compare(x.StepIndex, y.StepIndex) })
	return recs, skipped, nil
}

const userRequestClose = "</USER_REQUEST>"

// wrapperRe matches the system blocks agy appends to a user input: local
// time and editor state, model-setting changes.
var wrapperRe = regexp.MustCompile(`(?s)<(ADDITIONAL_METADATA|USER_SETTINGS_CHANGE)>.*?</(ADDITIONAL_METADATA|USER_SETTINGS_CHANGE)>`)

// userText extracts what the user typed from a USER_INPUT content: the
// <USER_REQUEST> blocks when present, else the content without agy's
// metadata blocks. agy truncates a very long input even in the full log,
// cutting off the closing tag, so an unclosed block runs to the end.
func userText(content string) string {
	if !strings.Contains(content, userRequestOpen) {
		return strings.TrimSpace(wrapperRe.ReplaceAllString(content, ""))
	}
	var parts []string
	rest := content
	for {
		_, after, ok := strings.Cut(rest, userRequestOpen)
		if !ok {
			break
		}
		body, next, closed := strings.Cut(after, userRequestClose)
		if t := strings.TrimSpace(body); t != "" {
			parts = append(parts, t)
		}
		if !closed {
			break
		}
		rest = next
	}
	return strings.Join(parts, "\n\n")
}

// scanHead reads a transcript only as far as the list row needs: the first
// user request and, when wantCwd, the first command's working directory.
func scanHead(ctx context.Context, path string, wantCwd bool) (title, cwd string) {
	seq, closeFn, err := jsonl.Open(path)
	if err != nil {
		return "", ""
	}
	defer closeFn()
	for _, raw := range seq {
		if ctx.Err() != nil || (title != "" && (cwd != "" || !wantCwd)) {
			break
		}
		var r record
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		if title == "" && r.Type == typeUserInput {
			title = userText(r.Content)
		}
		if wantCwd && cwd == "" {
			r.ToolCalls = unwrapCalls(r.ToolCalls)
			cwd = commandCwd(r)
		}
	}
	return title, cwd
}

// commandCwd is the first Cwd argument among a record's tool calls.
func commandCwd(r record) string {
	for _, tc := range r.ToolCalls {
		if cwd := argString(tc, "Cwd"); cwd != "" {
			return cwd
		}
	}
	return ""
}

func unwrapCalls(calls []toolCall) []toolCall {
	for i := range calls {
		calls[i].Args = unwrapArgs(calls[i].Args)
	}
	return calls
}

// firstRequest is the title fallback: the first non-empty user text.
func firstRequest(recs []record) string {
	for _, r := range recs {
		if r.Type == typeUserInput {
			if t := userText(r.Content); t != "" {
				return t
			}
		}
	}
	return ""
}

// stampLine matches the "Created At:/Completed At:" lines agy prefixes to
// every tool result.
var stampLine = regexp.MustCompile(`^(Created|Completed) At: \S+$`)

// resultText drops the leading timestamp lines from a tool result.
func resultText(content string) string {
	rest := content
	for {
		line, after, found := strings.Cut(rest, "\n")
		if !stampLine.MatchString(strings.TrimSpace(line)) {
			return strings.TrimSpace(rest)
		}
		if !found {
			return ""
		}
		rest = after
	}
}

// callInput is the call's arguments as JSON, without agy's UI-only
// toolAction/toolSummary fields.
func callInput(tc toolCall) string {
	args := make(map[string]json.RawMessage, len(tc.Args))
	for k, v := range tc.Args {
		if k != toolAction && k != toolSummary {
			args[k] = v
		}
	}
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

// unwrapArgs undoes the Antigravity app's double encoding: it stores every
// argument as a string holding the value's JSON ("\"/path\"", "500",
// "true"), where agy stores the value itself. A string that is not JSON is
// kept as written.
func unwrapArgs(args map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(args))
	for k, v := range args {
		var inner string
		if json.Unmarshal(v, &inner) == nil && json.Valid([]byte(inner)) {
			out[k] = json.RawMessage(inner)
			continue
		}
		out[k] = v
	}
	return out
}

// argString reads one string argument; "" when absent or not a string.
func argString(tc toolCall, key string) string {
	var s string
	if raw, ok := tc.Args[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
