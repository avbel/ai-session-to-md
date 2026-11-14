package handoff

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	tldrMaxRunes = 140
	warnWords    = 1200
	rerollWords  = 2400
)

// ErrRerollCancelled marks that the Reroll callback stopped because its
// context was cancelled (Ctrl-C) rather than because the LLM call failed.
// Validate surfaces it in ValidationResult.Err instead of swallowing it into
// the warnings list. The CLI detects it with errors.Is(result.Err,
// ErrRerollCancelled) and should exit 130 for context.Canceled (a
// DeadlineExceeded from the per-request LLM timeout stays visible inside the
// wrapped error, so the CLI can still map it to a runtime exit).
var ErrRerollCancelled = errors.New("summary re-roll cancelled")

var requiredHeadings = []string{
	"### Goal",
	"### Current state",
	"### Done so far",
	"### Decisions and rationale",
	"### Tried and failed",
	"### Next steps",
	"### Open questions and blockers",
	"### Key files, commands and references",
}

var absolutePathHints = []string{"/Users/", "/home/", `C:\`}

// Redactor is the seam to the secret patterns: wiring passes a func that
// applies the project's redaction (including home dir → "~") to text.
type Redactor func(text string) string

// ValidateOptions configures one validation run. Reroll, when set, is called
// at most once with the missing parts and must return the complete Summary
// again. Redact may be nil, which skips the secret check.
type ValidateOptions struct {
	Redact      Redactor
	Status      string
	PendingTool string
	Reroll      func(missing []string) (string, error)
}

// ValidationResult reports the accepted output. Missing is populated only
// when a re-roll was needed but no Reroll func was given; it feeds
// RerollMessage. After a performed re-roll, every still-failed check —
// including those that triggered the re-roll — is reported as a warning.
// Err is set only when the re-roll stopped because its context was cancelled
// (see ErrRerollCancelled); it never carries ordinary LLM failures, which
// stay warnings so a broken answer is still accepted with diagnostics.
type ValidationResult struct {
	Clean    string
	Warnings []string
	ReRolled bool
	Missing  []string
	Err      error
}

type checkFailure struct {
	key     string
	message string
	reroll  bool
}

// Validate checks an LLM Summary against the required output shape. Checks
// a–c, and a word count over 2400, trigger one re-roll when Reroll is set;
// checks d–g report warnings. Check e replaces the output with its redacted
// version when redaction changes it.
func Validate(output string, opts ValidateOptions) ValidationResult {
	clean, fails := validateOnce(output, opts)
	res := ValidationResult{Clean: clean}
	needsReroll := false
	for _, f := range fails {
		res.Warnings = append(res.Warnings, f.message)
		if f.reroll {
			needsReroll = true
		}
	}
	if !needsReroll {
		return res
	}
	if opts.Reroll == nil {
		res.Missing = missingKeys(fails)
		return res
	}
	res.ReRolled = true
	again, err := opts.Reroll(missingKeys(fails))
	if err != nil {
		if isCancel(err) {
			res.Err = fmt.Errorf("%w: %w", ErrRerollCancelled, err)
			return res
		}
		res.Warnings = append(res.Warnings, "re-roll request failed: "+err.Error())
		return res
	}
	clean2, fails2 := validateOnce(again, opts)
	res.Clean = clean2
	res.Warnings = nil
	for _, f := range fails2 {
		res.Warnings = append(res.Warnings, f.message)
	}
	return res
}

// isCancel reports whether err is a context cancellation or deadline
// overrun, which the Reroll callback may return wrapped in its own error.
func isCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func missingKeys(fails []checkFailure) []string {
	var keys []string
	for _, f := range fails {
		if f.reroll && !slices.Contains(keys, f.key) {
			keys = append(keys, f.key)
		}
	}
	return keys
}

// validateOnce runs every check once over the stripped output, returning the
// accepted (possibly redacted) text and all failures.
func validateOnce(output string, opts ValidateOptions) (string, []checkFailure) {
	clean := strings.TrimSpace(stripOuterFence(output))
	var fails []checkFailure

	if opts.Redact != nil {
		if redacted := opts.Redact(clean); redacted != clean {
			fails = append(fails, checkFailure{"", "output contained unredacted secrets; the redacted version was kept", false})
			clean = redacted
		}
	}

	s := scanOutput(clean)
	fails = append(fails, checkTLDR(clean, s)...)
	fails = append(fails, checkHeadings(s)...)
	fails = append(fails, checkNextSteps(s)...)
	fails = append(fails, checkPathsAndWords(clean)...)
	fails = append(fails, checkStop(s, opts)...)
	return clean, fails
}

type heading struct {
	idx  int
	text string
}

// scanOutput classifies the non-fenced lines of an output: TL;DR lines,
// heading lines, the first non-empty line and the "### Next steps" position.
// Fence tracking uses the opening run length, so ``` blocks inside ````-fenced
// verbatim quotes are not mistaken for fence boundaries.
type scanOutputResult struct {
	tldrIdxs      []int
	tldrTexts     []string
	headings      []heading
	firstNonEmpty int
	nextSteps     int
	nextStepsBody string
}

func scanOutput(clean string) scanOutputResult {
	s := scanOutputResult{firstNonEmpty: -1, nextSteps: -1}
	inFence, openRun := false, 0
	wantBody := false
	for i, line := range strings.Split(clean, "\n") {
		t := strings.TrimSpace(line)
		run := backtickRun(t)
		switch {
		case !inFence && run >= 3:
			inFence, openRun = true, run
		case inFence && run == openRun:
			inFence = false
		case inFence:
		default:
			if t == "" {
				continue
			}
			if s.firstNonEmpty < 0 {
				s.firstNonEmpty = i
			}
			if strings.HasPrefix(t, "TL;DR:") {
				s.tldrIdxs = append(s.tldrIdxs, i)
				s.tldrTexts = append(s.tldrTexts, strings.TrimSpace(strings.TrimPrefix(t, "TL;DR:")))
			}
			if isMarkdownHeading(t) {
				s.headings = append(s.headings, heading{i, t})
				wantBody = t == "### Next steps"
				if wantBody {
					s.nextSteps = i
				}
				continue
			}
			if wantBody {
				s.nextStepsBody = t
				wantBody = false
			}
		}
	}
	return s
}

// isMarkdownHeading matches only ATX headings of level 1–3: one to three '#'
// followed by a space. "#123" issue references and "#### x" notes are plain
// text, not headings, and must not trigger a re-roll.
func isMarkdownHeading(t string) bool {
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n >= 1 && n <= 3 && n < len(t) && t[n] == ' '
}

// stripOuterFence removes one code fence that wraps the whole answer. The
// closing fence must be the last line and must match the opening run exactly;
// an inner fence of the same run length makes the wrap ambiguous, so nothing
// is stripped then.
func stripOuterFence(output string) string {
	trimmed := strings.Trim(output, " \t\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		return trimmed
	}
	open := strings.TrimSpace(lines[0])
	run := backtickRun(open)
	if run < 3 || strings.Contains(open[run:], "`") {
		return trimmed
	}
	for i := 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if backtickRun(t) != run || len(t) != run {
			continue
		}
		if i != len(lines)-1 {
			return trimmed
		}
		return strings.Join(lines[1:i], "\n")
	}
	return trimmed
}

func backtickRun(s string) int {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	return n
}

func checkTLDR(clean string, s scanOutputResult) []checkFailure {
	switch len(s.tldrIdxs) {
	case 0:
		return []checkFailure{{"TL;DR:", "no TL;DR: line", true}}
	case 1:
	default:
		return []checkFailure{{"TL;DR:", fmt.Sprintf("expected exactly one TL;DR: line, found %d", len(s.tldrIdxs)), true}}
	}
	var fails []checkFailure
	text := s.tldrTexts[0]
	switch {
	case text == "":
		fails = append(fails, checkFailure{"TL;DR:", "TL;DR: line is empty", true})
	case utf8.RuneCountInString(text) > tldrMaxRunes:
		fails = append(fails, checkFailure{"TL;DR:",
			fmt.Sprintf("TL;DR: line is %d chars, over the %d limit", utf8.RuneCountInString(text), tldrMaxRunes), true})
	}
	if s.firstNonEmpty != s.tldrIdxs[0] {
		fails = append(fails, checkFailure{"TL;DR:", "content appears before the TL;DR: line", true})
	}
	return fails
}

func checkHeadings(s scanOutputResult) []checkFailure {
	var fails []checkFailure
	expected := 0
	for _, h := range s.headings {
		if expected < len(requiredHeadings) && h.text == requiredHeadings[expected] {
			expected++
			continue
		}
		if slices.Contains(requiredHeadings, h.text) {
			fails = append(fails, checkFailure{h.text, fmt.Sprintf("heading %q is out of order or duplicated", h.text), true})
		} else {
			fails = append(fails, checkFailure{h.text, fmt.Sprintf("unexpected heading %q; only the 8 required ### headings are allowed", h.text), true})
		}
	}
	for _, req := range requiredHeadings[expected:] {
		fails = append(fails, checkFailure{req, fmt.Sprintf("missing heading %q", req), true})
	}
	return fails
}

// checkNextSteps reports when the "### Next steps" body does not start with
// item 1.
func checkNextSteps(s scanOutputResult) []checkFailure {
	if s.nextSteps < 0 {
		return nil
	}
	if !strings.HasPrefix(s.nextStepsBody, "1.") {
		return []checkFailure{{"### Next steps", `### Next steps body must start with "1."`, true}}
	}
	return nil
}

func checkPathsAndWords(clean string) []checkFailure {
	var fails []checkFailure
	for _, p := range absolutePathHints {
		if strings.Contains(clean, p) {
			fails = append(fails, checkFailure{"", fmt.Sprintf("output contains absolute path %q; use repository-relative paths", p), false})
		}
	}
	switch n := len(strings.Fields(clean)); {
	case n > rerollWords:
		fails = append(fails, checkFailure{"", fmt.Sprintf("summary is %d words, over the %d re-roll limit", n, rerollWords), true})
	case n > warnWords:
		fails = append(fails, checkFailure{"", fmt.Sprintf("summary is %d words, over the %d target", n, warnWords), false})
	}
	return fails
}

var stopStatusPrefixes = []string{"interrupted by user", "stopped during tool call", "stopped mid-turn"}

func isStopStatus(status string) bool {
	for _, p := range stopStatusPrefixes {
		if strings.HasPrefix(status, p) {
			return true
		}
	}
	return false
}

func checkStop(s scanOutputResult, opts ValidateOptions) []checkFailure {
	if !isStopStatus(opts.Status) || s.nextSteps < 0 {
		return nil
	}
	low := strings.ToLower(s.nextStepsBody)
	tool := strings.ToLower(opts.PendingTool)
	if strings.Contains(low, "verify") || strings.Contains(low, "check") ||
		(tool != "" && strings.Contains(low, tool)) {
		return nil
	}
	return []checkFailure{{"", "next step 1 does not address the pending tool call or its verification", false}}
}

// ExtractTLDR returns the text after the first TL;DR: line, or "".
func ExtractTLDR(output string) string {
	s := scanOutput(strings.TrimSpace(stripOuterFence(output)))
	if len(s.tldrIdxs) == 0 {
		return ""
	}
	return s.tldrTexts[0]
}
