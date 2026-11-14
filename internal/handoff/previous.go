package handoff

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avbel/ai-session-to-md/internal/session"
)

const (
	maxHistoryEntries = 20
	maxPrevLLMChars   = 12000
	maxCandidateBytes = 4 << 20
)

// candidateFiles are the handoff files this tool writes, one per harness.
var candidateFiles = []string{"CLAUDE_CODE.md", "CODEX.md", "OPEN_CODE.md"}

// OutputFileName maps a harness to its handoff file in the output dir.
func OutputFileName(h session.Harness) (string, bool) {
	switch h {
	case session.HarnessClaudeCode:
		return candidateFiles[0], true
	case session.HarnessCodex:
		return candidateFiles[1], true
	case session.HarnessOpenCode:
		return candidateFiles[2], true
	}
	return "", false
}

// Previous is one candidate handoff file found in the output dir.
type Previous struct {
	Name    string
	Meta    Meta
	Content string
}

// Selection is the outcome of scanning the output dir: the newest valid
// other-session file (Prev) and, when present, the file of the session being
// handed off (SameSession), which is never appended as a hop of its own.
type Selection struct {
	Prev        *Previous
	SameSession *Previous
}

// LogFunc receives --verbose diagnostics about ignored candidates.
type LogFunc func(string)

// Select reads the candidate handoff files from outputDir through an
// os.Root confined to that directory. Candidates whose first line is not a
// parsable meta comment are ignored with a log. The newest other-session
// candidate by meta generated_at (not mtime) becomes Prev; a candidate whose
// session_id equals currentSessionID becomes SameSession.
func Select(outputDir, currentSessionID string, log LogFunc) (Selection, error) {
	if log == nil {
		log = func(string) {}
	}
	root, err := os.OpenRoot(outputDir)
	if err != nil {
		return Selection{}, fmt.Errorf("open output dir %s: %w", outputDir, err)
	}
	defer func() { _ = root.Close() }()

	var sel Selection
	for _, name := range candidateFiles {
		prev, err := readCandidate(root, name)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log(fmt.Sprintf("ignoring candidate %s: %v", name, err))
			}
			continue
		}
		if prev.Meta.SessionID == currentSessionID {
			if newer(prev, sel.SameSession) {
				sel.SameSession = prev
			}
			continue
		}
		if newer(prev, sel.Prev) {
			sel.Prev = prev
		}
	}
	return sel, nil
}

func readCandidate(root *os.Root, name string) (*Previous, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxCandidateBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	meta, ok := ParseMeta(FirstLine(string(data)))
	if !ok {
		return nil, errors.New("first line is not a valid ai-session-to-md meta comment")
	}
	return &Previous{Name: name, Meta: meta, Content: string(data)}, nil
}

func metaTime(m Meta) time.Time {
	t, err := time.Parse(time.RFC3339, m.GeneratedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

func newer(cand, cur *Previous) bool {
	if cur == nil {
		return true
	}
	return metaTime(cand.Meta).After(metaTime(cur.Meta))
}

// NewHistory builds the history rows for the next file: the same-session
// file's history (inheriting earlier hops of a re-run), then the previous
// file's history, then one entry for the previous file itself. Entries dedupe
// by (harness, session_id) keeping the first, and the last 20 are kept.
func (sel Selection) NewHistory() []HistoryEntry {
	var entries []HistoryEntry
	if sel.SameSession != nil {
		entries = append(entries, sel.SameSession.Meta.History...)
	}
	if sel.Prev != nil {
		entries = append(entries, sel.Prev.Meta.History...)
		entries = append(entries, HistoryEntry{
			Harness:     sel.Prev.Meta.Harness,
			SessionID:   sel.Prev.Meta.SessionID,
			GeneratedAt: sel.Prev.Meta.GeneratedAt,
			Host:        sel.Prev.Meta.Host,
			TLDR:        sel.Prev.Meta.TLDR,
		})
	}
	return capHistory(entries)
}

func capHistory(entries []HistoryEntry) []HistoryEntry {
	seen := make(map[string]struct{}, len(entries))
	unique := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		key := e.Harness + "\x00" + e.SessionID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, e)
	}
	if len(unique) > maxHistoryEntries {
		unique = unique[len(unique)-maxHistoryEntries:]
	}
	return unique
}

// ForLLM extracts what the summarizer sees from the previous file: its
// Summary section (or the raw-transcript section) plus the history table,
// at most maxPrevLLMChars chars. Injected reminders and HTML comments inside
// verbatim quoted blocks are stripped. The result must still be redacted by
// the wiring before it reaches the LLM; returns "" when there is no previous.
func (sel Selection) ForLLM() string {
	if sel.Prev == nil {
		return ""
	}
	content := sanitizeVerbatim(sel.Prev.Content)
	var parts []string
	summary := extractSection(content, "## Summary")
	if summary == "" {
		summary = extractSection(content, rawTranscriptHeading)
	}
	if summary != "" {
		parts = append(parts, summary)
	}
	if hist := extractSection(content, historyHeading); hist != "" {
		parts = append(parts, hist)
	}
	return capRunes(strings.Join(parts, "\n\n"), maxPrevLLMChars)
}

func capRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

var (
	systemReminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	htmlCommentRe    = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// sanitizeVerbatim strips a UTF-8 BOM and every \r, then removes injected
// <system-reminder> spans and <!-- --> comments, but only inside fenced
// verbatim blocks, where such text is quoted data.
func sanitizeVerbatim(content string) string {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r", "")
	var b, seg strings.Builder
	inFence, openRun := false, 0
	flush := func() {
		clean := systemReminderRe.ReplaceAllString(seg.String(), "")
		clean = htmlCommentRe.ReplaceAllString(clean, "")
		b.WriteString(clean)
		seg.Reset()
	}
	for line := range strings.Lines(content) {
		t := strings.TrimSpace(line)
		run := backtickRun(t)
		switch {
		case !inFence && run >= 3:
			inFence, openRun = true, run
			b.WriteString(line)
		case inFence && run == openRun:
			flush()
			inFence = false
			b.WriteString(line)
		case inFence:
			seg.WriteString(line)
		default:
			b.WriteString(line)
		}
	}
	if inFence {
		flush()
	}
	return b.String()
}

// extractSection returns the lines of the section starting at heading (a
// "## " line matched exactly), up to the next "## " heading, heading included.
func extractSection(content, heading string) string {
	lines := strings.Split(content, "\n")
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
	var b strings.Builder
	for i := start; i < len(lines); i++ {
		if i > start && strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			break
		}
		b.WriteString(lines[i])
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
