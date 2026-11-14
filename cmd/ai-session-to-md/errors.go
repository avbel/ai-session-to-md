package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/llm"
	"github.com/avbel/ai-session-to-md/internal/ui"
)

// hintError carries a user-facing failure plus an actionable hint; the error
// print renders it as "✗ <msg>" followed by "  hint: <hint>". cause keeps the
// wrapped error's exit-code mapping intact.
type hintError struct {
	msg   string
	hint  string
	cause error
}

func (e *hintError) Error() string { return e.msg }

func (e *hintError) Unwrap() error { return e.cause }

// printError renders a failure as "✗ <what happened>" with an optional
// "  hint: <what to do>" line. It never decides the exit code; exitFor does.
func printError(u *ui.UI, err error) {
	var h *hintError
	switch {
	case errors.As(err, &h):
		u.Fail(h.msg)
		if h.hint != "" {
			u.Hint(h.hint)
		}
	default:
		msg, hint := classify(err)
		u.Fail(msg)
		if hint != "" {
			u.Hint(hint)
		}
	}
}

// classify derives the message and hint for errors that carry no hintError
// of their own: LLM HTTP failures, timeouts, the too-large transcript and
// ambiguous id prefixes.
func classify(err error) (string, string) {
	var apiErr *llm.APIError
	var amb harness.ErrAmbiguous
	switch {
	case errors.As(err, &apiErr):
		msg := fmt.Sprintf("LLM request failed: HTTP %d", apiErr.Status)
		if m := errorMessage(apiErr.Body); m != "" {
			msg += ": " + m
		}
		return msg, apiHint(apiErr.Status)
	case errors.Is(err, errLLM) && errors.Is(err, context.DeadlineExceeded):
		return "LLM request timed out", "raise --timeout (it applies per HTTP request) and retry"
	case errors.Is(err, errLLM) && strings.Contains(err.Error(), "transcript too large"):
		// reduce's error carries no counts yet, so the numbers stay behind.
		return "transcript too large for --max-input-tokens", "raise --max-input-tokens or run with --raw"
	case errors.As(err, &amb):
		return err.Error(), "pass more characters of the session id"
	default:
		return err.Error(), ""
	}
}

// apiHint names the likely fix for one HTTP status.
func apiHint(status int) string {
	switch status {
	case 401:
		return "check the API key named by --api-key-env, or run with --raw"
	case 404:
		return "check --base-url and --model, or run with --raw"
	case 429:
		return "the endpoint is rate limiting; retry later, or run with --raw"
	default:
		return "run with --raw to skip the LLM"
	}
}

// errorMessage pulls a human message out of an LLM error body when it is
// parseable JSON: error.message, then error (string), then message.
func errorMessage(body string) string {
	if body == "" {
		return ""
	}
	var doc struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal([]byte(body), &doc) != nil {
		return ""
	}
	if len(doc.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(doc.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
		var s string
		if json.Unmarshal(doc.Error, &s) == nil && s != "" {
			return s
		}
	}
	return doc.Message
}
