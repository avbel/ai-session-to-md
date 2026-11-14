//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/handoff"
)

// readHandoff reads a handoff file and parses its meta from the first line,
// applying the same rules as the tool itself.
func readHandoff(t *testing.T, path string) (handoff.Meta, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read handoff %s: %v", path, err)
	}
	doc := string(data)
	meta, ok := handoff.ParseMeta(handoff.FirstLine(doc))
	if !ok {
		t.Fatalf("%s: first line is not a parsable meta comment:\n%s", path, handoff.FirstLine(doc))
	}
	return meta, doc
}

// section returns the lines of a "## " section, heading included, up to the
// next "## " heading.
func section(doc, heading string) string {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// historyRows returns the data rows of the handoff history table, oldest
// first.
func historyRows(doc string) []string {
	var rows []string
	for _, line := range strings.Split(section(doc, "## Handoff history"), "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| # ") || strings.HasPrefix(line, "|---") {
			continue
		}
		rows = append(rows, line)
	}
	return rows
}

// statusLine returns the "- **Status:**" bullet of a document.
func statusLine(doc string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "- **Status:**") {
			return line
		}
	}
	return ""
}

// mustContain fails when s does not contain sub.
func mustContain(t *testing.T, s, sub, what string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%s does not contain %q\n--- content ---\n%s", what, sub, s)
	}
}

// mustNotContain fails when s contains sub.
func mustNotContain(t *testing.T, s, sub, what string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("%s contains %q, want it absent\n--- content ---\n%s", what, sub, s)
	}
}

// mustEqual fails when got and want differ.
func mustEqual(t *testing.T, got, want, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

// wantExit fails unless the run exited with the expected code.
func wantExit(t *testing.T, res runResult, want int) {
	t.Helper()
	if res.Code != want {
		t.Fatalf("exit = %d, want %d\nstderr:\n%s", res.Code, want, res.Stderr)
	}
}

// waitNextSecond blocks until the wall clock's second changes, so consecutive
// handoff files get strictly increasing RFC3339 generated_at values (the meta
// stores whole seconds).
func waitNextSecond() {
	start := time.Now().Unix()
	for time.Now().Unix() == start {
		time.Sleep(10 * time.Millisecond)
	}
}

// handoffPath is the default output path for a harness file name.
func handoffPath(e *testEnv, name string) string {
	return filepath.Join(e.repo, name)
}
