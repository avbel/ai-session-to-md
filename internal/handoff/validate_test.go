package handoff

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const goodSummary = `TL;DR: Make the jsonl reader handle huge lines; stopped after tests failed; rerun go test next.
### Goal
Let the tool read multi-megabyte session lines without failing.
### Current state
The reader is written; one test fails on a 2 MB line.
### Done so far
- Wrote internal/jsonl/reader.go with ReadBytes.
- Added the zstd passthrough.
### Decisions and rationale
- bufio.Reader.ReadBytes because Scanner caps tokens at 64k.
### Tried and failed
- bufio.Scanner → "token too long" error.
### Next steps
1. Run go test ./internal/jsonl/... and fix the failing huge-line case.
2. Then wire the reader into the codex adapter.
### Open questions and blockers
none
### Key files, commands and references
- internal/jsonl/reader.go`

func summaryWithWords(n int) string {
	head := "TL;DR: word count test summary.\n### Goal\n"
	tail := "\n### Current state\nnone\n### Done so far\nnone\n### Decisions and rationale\nnone\n" +
		"### Tried and failed\nnone\n### Next steps\n1. Do the next thing.\n" +
		"### Open questions and blockers\nnone\n### Key files, commands and references\nnone"
	used := len(strings.Fields(head)) + len(strings.Fields(tail))
	fill := make([]string, max(0, n-used))
	for i := range fill {
		fill[i] = fmt.Sprintf("word%d", i)
	}
	return head + strings.Join(fill, " ") + tail
}

func TestValidateGoodSummary(t *testing.T) {
	res := Validate(goodSummary, ValidateOptions{})
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
	if len(res.Missing) != 0 {
		t.Errorf("unexpected missing: %v", res.Missing)
	}
	if res.ReRolled {
		t.Error("good summary must not re-roll")
	}
	if res.Clean != goodSummary {
		t.Errorf("clean output changed:\n%s", res.Clean)
	}
}

func TestValidateTLDRChecks(t *testing.T) {
	t.Run("two tldr lines", func(t *testing.T) {
		out := "TL;DR: first one.\n" + goodSummary
		res := Validate(out, ValidateOptions{})
		if !containsString(res.Missing, "TL;DR:") {
			t.Errorf("missing should contain TL;DR:, got %v", res.Missing)
		}
		if len(res.Warnings) == 0 {
			t.Error("expected a warning")
		}
	})
	t.Run("no tldr line", func(t *testing.T) {
		out := strings.Replace(goodSummary, "TL;DR: Make the jsonl reader handle huge lines; stopped after tests failed; rerun go test next.\n", "", 1)
		res := Validate(out, ValidateOptions{})
		if !containsString(res.Missing, "TL;DR:") {
			t.Errorf("missing should contain TL;DR:, got %v", res.Missing)
		}
	})
	t.Run("empty tldr", func(t *testing.T) {
		out := strings.Replace(goodSummary,
			"TL;DR: Make the jsonl reader handle huge lines; stopped after tests failed; rerun go test next.",
			"TL;DR:", 1)
		res := Validate(out, ValidateOptions{})
		if !containsString(res.Missing, "TL;DR:") {
			t.Errorf("missing should contain TL;DR:, got %v", res.Missing)
		}
	})
	t.Run("too long tldr", func(t *testing.T) {
		long := strings.Repeat("x", tldrMaxRunes+1)
		out := strings.Replace(goodSummary,
			"TL;DR: Make the jsonl reader handle huge lines; stopped after tests failed; rerun go test next.",
			"TL;DR: "+long, 1)
		res := Validate(out, ValidateOptions{})
		if !containsString(res.Missing, "TL;DR:") {
			t.Errorf("missing should contain TL;DR:, got %v", res.Missing)
		}
	})
}

func containsString(list []string, s string) bool {
	return slices.Contains(list, s)
}

func TestValidatePreambleBeforeTLDR(t *testing.T) {
	out := "Here is the summary you asked for.\n\n" + goodSummary
	res := Validate(out, ValidateOptions{})
	if !containsString(res.Missing, "TL;DR:") {
		t.Errorf("missing should contain TL;DR:, got %v", res.Missing)
	}
}

func TestValidateMissingHeading(t *testing.T) {
	out := strings.Replace(goodSummary,
		"### Tried and failed\n- bufio.Scanner → \"token too long\" error.\n", "", 1)
	res := Validate(out, ValidateOptions{})
	if !containsString(res.Missing, "### Tried and failed") {
		t.Errorf("missing should contain the heading, got %v", res.Missing)
	}
}

func TestValidateExtraHeading(t *testing.T) {
	out := strings.Replace(goodSummary, "### Goal\n", "# Overview\n### Goal\n", 1)
	res := Validate(out, ValidateOptions{})
	if !containsString(res.Missing, "# Overview") {
		t.Errorf("missing should contain the extra heading, got %v", res.Missing)
	}
}

func TestValidateHeadingOrder(t *testing.T) {
	out := strings.Replace(goodSummary, "### Current state\nThe reader is written; one test fails on a 2 MB line.\n", "", 1)
	out = strings.Replace(out, "### Next steps\n",
		"### Current state\nThe reader is written; one test fails on a 2 MB line.\n### Next steps\n", 1)
	res := Validate(out, ValidateOptions{})
	if len(res.Missing) == 0 {
		t.Error("out-of-order headings must fail")
	}
}

func TestValidateNextStepsNumbering(t *testing.T) {
	out := strings.Replace(goodSummary, "1. Run go test", "2. Run go test", 1)
	res := Validate(out, ValidateOptions{})
	if !containsString(res.Missing, "### Next steps") {
		t.Errorf("missing should contain ### Next steps, got %v", res.Missing)
	}
}

func TestValidateAbsolutePathsWarn(t *testing.T) {
	out := goodSummary + "\n- See also /Users/alice/notes.md\n"
	res := Validate(out, ValidateOptions{})
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "absolute path") {
		t.Errorf("expected one absolute-path warning, got %v", res.Warnings)
	}
	if len(res.Missing) != 0 {
		t.Errorf("warnings must not trigger a re-roll, got missing %v", res.Missing)
	}
	if !strings.Contains(res.Clean, "/Users/alice/notes.md") {
		t.Error("clean output must keep the text when no redactor is set")
	}
}

func TestValidateRedactorApplied(t *testing.T) {
	out := strings.Replace(goodSummary, "rerun go test next.", "rerun go test with token hush-secret-1 next.", 1)
	res := Validate(out, ValidateOptions{Redact: func(s string) string {
		return strings.ReplaceAll(s, "hush-secret-1", "[REDACTED]")
	}})
	if !strings.Contains(res.Clean, "[REDACTED]") || strings.Contains(res.Clean, "hush-secret-1") {
		t.Errorf("clean output must be the redacted version:\n%s", res.Clean)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "redacted") {
		t.Errorf("expected one redaction warning, got %v", res.Warnings)
	}
}

func TestValidateWordCountWarnAndReroll(t *testing.T) {
	res := Validate(summaryWithWords(1300), ValidateOptions{})
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "1300 words") {
		t.Errorf("expected a word-count warning, got %v", res.Warnings)
	}
	if len(res.Missing) != 0 {
		t.Errorf("1300 words must not trigger a re-roll, got %v", res.Missing)
	}

	res = Validate(summaryWithWords(2500), ValidateOptions{})
	if len(res.Missing) == 0 {
		t.Error("2500 words must trigger a re-roll")
	}
}

func TestValidateStopStatusCheck(t *testing.T) {
	t.Run("unaddressed pending tool", func(t *testing.T) {
		res := Validate(goodSummary, ValidateOptions{Status: "stopped during tool call Bash", PendingTool: "Bash"})
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "next step 1") {
			t.Errorf("expected a stop-status warning, got %v", res.Warnings)
		}
	})
	t.Run("mentions tool name", func(t *testing.T) {
		out := strings.Replace(goodSummary, "1. Run go test ./internal/jsonl/... and fix the failing huge-line case.",
			"1. Run git status to confirm the Bash call finished, then go test ./internal/jsonl/....", 1)
		res := Validate(out, ValidateOptions{Status: "stopped during tool call Bash", PendingTool: "Bash"})
		if len(res.Warnings) != 0 {
			t.Errorf("unexpected warnings: %v", res.Warnings)
		}
	})
	t.Run("interrupted mentions check", func(t *testing.T) {
		out := strings.Replace(goodSummary, "1. Run go test ./internal/jsonl/... and fix the failing huge-line case.",
			"1. Check which files were already written before continuing.", 1)
		res := Validate(out, ValidateOptions{Status: "interrupted by user"})
		if len(res.Warnings) != 0 {
			t.Errorf("unexpected warnings: %v", res.Warnings)
		}
	})
	t.Run("not a stop status", func(t *testing.T) {
		res := Validate(goodSummary, ValidateOptions{Status: "turn completed"})
		if len(res.Warnings) != 0 {
			t.Errorf("unexpected warnings: %v", res.Warnings)
		}
	})
}

func TestValidateStripsOuterFence(t *testing.T) {
	for _, wrap := range []struct{ open, close string }{
		{"```markdown", "```"},
		{"```", "```"},
		{"````", "````"},
	} {
		out := wrap.open + "\n" + goodSummary + "\n" + wrap.close
		res := Validate(out, ValidateOptions{})
		if res.Clean != goodSummary {
			t.Errorf("fence %q not stripped cleanly:\n%s", wrap.open, res.Clean)
		}
		if len(res.Warnings) != 0 {
			t.Errorf("fence %q: unexpected warnings %v", wrap.open, res.Warnings)
		}
	}
}

func TestValidateDoesNotStripInnerCodeBlock(t *testing.T) {
	out := goodSummary + "\n```bash\ngo test ./...\n```"
	res := Validate(out, ValidateOptions{})
	if res.Clean != out {
		t.Error("a trailing code block is not an outer fence and must be kept")
	}
}

func TestValidateRerollOnce(t *testing.T) {
	calls := 0
	res := Validate("no structure at all\n", ValidateOptions{
		Reroll: func(missing []string) (string, error) {
			calls++
			if len(missing) == 0 {
				t.Error("reroll must receive the missing parts")
			}
			return goodSummary, nil
		},
	})
	if calls != 1 {
		t.Errorf("expected exactly one re-roll, got %d", calls)
	}
	if !res.ReRolled {
		t.Error("ReRolled must be true")
	}
	if res.Clean != goodSummary {
		t.Errorf("clean must be the re-rolled output:\n%s", res.Clean)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
}

func TestValidateRerollStillBrokenAcceptedWithWarnings(t *testing.T) {
	calls := 0
	res := Validate("broken\n", ValidateOptions{
		Reroll: func(missing []string) (string, error) {
			calls++
			return "still broken\n", nil
		},
	})
	if calls != 1 {
		t.Errorf("at most one re-roll is allowed, got %d", calls)
	}
	if !res.ReRolled {
		t.Error("ReRolled must be true")
	}
	if res.Clean != "still broken" {
		t.Errorf("a failing second answer is accepted as-is:\n%s", res.Clean)
	}
	if len(res.Warnings) == 0 {
		t.Error("failed checks must be reported as warnings")
	}
}

func TestValidateRerollErrorKeepsFirstAnswer(t *testing.T) {
	res := Validate("broken\n", ValidateOptions{
		Reroll: func(missing []string) (string, error) {
			return "", fmt.Errorf("LLM unreachable")
		},
	})
	if !res.ReRolled {
		t.Error("ReRolled must be true even when the re-roll fails")
	}
	if res.Clean != "broken" {
		t.Errorf("the first answer must be kept on re-roll error:\n%s", res.Clean)
	}
	if !containsString(res.Warnings, "re-roll request failed: LLM unreachable") {
		t.Errorf("the re-roll error must be reported, got %v", res.Warnings)
	}
}

func TestExtractTLDR(t *testing.T) {
	got := ExtractTLDR("```markdown\n" + goodSummary + "\n```")
	want := "Make the jsonl reader handle huge lines; stopped after tests failed; rerun go test next."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if ExtractTLDR("nothing here") != "" {
		t.Error("no TL;DR line must yield an empty string")
	}
}

func TestRerollMessage(t *testing.T) {
	msg := RerollMessage([]string{"TL;DR:", "### Tried and failed"})
	if !strings.Contains(msg, "TL;DR:, ### Tried and failed") {
		t.Errorf("missing list not interpolated:\n%s", msg)
	}
	if !strings.Contains(msg, "COMPLETE Summary") {
		t.Errorf("reroll prompt text changed:\n%s", msg)
	}
}

func TestTruncateChunkNotesBulletBoundary(t *testing.T) {
	notes := "#### Goal and user instructions\n- one two three\n- four five six seven\n#### Decisions\n- eight nine ten eleven twelve thirteen fourteen\n"
	got := TruncateChunkNotes(notes, 10)
	if strings.Contains(got, "four five six seven") || strings.Contains(got, "eight") {
		t.Errorf("the first overflowing bullet must be dropped whole:\n%s", got)
	}
	if !strings.Contains(got, "one two three") {
		t.Errorf("bullets that fit must be kept:\n%s", got)
	}
	if unchanged := TruncateChunkNotes(notes, 100); unchanged != notes {
		t.Errorf("notes under the limit must be returned unchanged:\n%s", unchanged)
	}
}
