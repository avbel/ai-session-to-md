package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/jsonl"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// Load reads one session file and returns the normalized session built
// from the entries on the active branch chain.
func (a *Adapter) Load(ctx context.Context, m session.Meta) (session.Session, error) {
	path, err := a.resolve(m)
	if err != nil {
		return session.Session{}, err
	}
	lines, skipped, err := parseLines(ctx, path)
	if err != nil {
		return session.Session{}, fmt.Errorf("parse claude session %s: %w", path, err)
	}
	id := m.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	title := m.Title
	if title == "" {
		title = deriveTitle(lines)
	}
	updatedAt := m.UpdatedAt
	if st, err := os.Stat(path); err == nil {
		updatedAt = st.ModTime()
	}
	s := session.Session{
		Skipped:   skipped,
		Harness:   session.HarnessClaudeCode,
		ID:        id,
		Source:    path,
		Title:     title,
		UpdatedAt: updatedAt,
	}
	for _, i := range activeChain(lines) {
		switch lines[i].Type {
		case typeUser:
			s.Turns = append(s.Turns, userTurns(lines[i])...)
		case typeAssistant:
			s.Turns = append(s.Turns, assistantTurns(lines[i])...)
		}
	}
	applyFacts(&s, lines)
	return s, nil
}

// deriveTitle recomputes the list title when the caller passes a Meta
// without one: the last custom, ai, or prompt title, else the first user
// text on the active branch.
func deriveTitle(lines []line) string {
	var custom, ai, prompt string
	for _, ln := range lines {
		switch {
		case ln.CustomTitle != "":
			custom = ln.CustomTitle
		case ln.AITitle != "":
			ai = ln.AITitle
		case ln.LastPrompt != "":
			prompt = ln.LastPrompt
		}
	}
	switch {
	case custom != "":
		return custom
	case ai != "":
		return ai
	case prompt != "":
		return prompt
	}
	for _, ln := range lines {
		if !ln.IsSidechain && ln.Type == typeUser && !ln.IsMeta && !ln.IsCompactSummary {
			for _, t := range userTurns(ln) {
				if t.Kind == session.UserText {
					return t.Text
				}
			}
		}
	}
	return ""
}

// resolve finds the transcript file: the recorded source path when it still
// exists, else the id directly inside any project dir.
func (a *Adapter) resolve(m session.Meta) (string, error) {
	if m.Source != "" {
		if st, err := os.Stat(m.Source); err == nil && !st.IsDir() {
			return m.Source, nil
		}
	}
	id := m.ID
	if id == "" {
		return "", fmt.Errorf("claude: session id is empty")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", fmt.Errorf("claude: invalid session id %q", id)
	}
	hits, err := filepath.Glob(filepath.Join(a.dir, "projects", "*", id+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob claude sessions for %s: %w", id, err)
	}
	if len(hits) == 0 {
		return "", fmt.Errorf("claude: session %s not found", id)
	}
	return hits[0], nil
}

// parseLines unmarshals every line; malformed and typeless lines are
// counted in skipped, never fatal.
func parseLines(ctx context.Context, path string) ([]line, int, error) {
	seq, closeFn, err := jsonl.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open claude session %s: %w", path, err)
	}
	defer closeFn()
	var lines []line
	skipped := 0
	for _, raw := range seq {
		if err := ctx.Err(); err != nil {
			return nil, skipped, fmt.Errorf("load claude session: %w", err)
		}
		var ln line
		if json.Unmarshal(raw, &ln) != nil || ln.Type == "" {
			skipped++
			continue
		}
		lines = append(lines, ln)
	}
	return lines, skipped, nil
}

// applyFacts walks all lines in file order and fills cwd, branch, version,
// model, todos and files touched. Sidechain entries are excluded from
// tool-use derived facts; cwd, branch and version come from any entry.
func applyFacts(s *session.Session, lines []line) {
	var ts todoScanner
	seen := make(map[string]bool)
	for _, ln := range lines {
		if ln.Cwd != "" {
			s.Cwd = ln.Cwd
		}
		if b := ln.GitBranch; b != "" && b != "HEAD" {
			s.Branch = b
		}
		if ln.Version != "" {
			s.HarnessVer = ln.Version
		}
		if ln.IsSidechain {
			continue
		}
		if ln.Type == typeAssistant && ln.Message.Model != "" {
			s.Model = ln.Message.Model
		}
		blocks := contentBlocks(ln.Message.Content)
		switch ln.Type {
		case typeAssistant:
			ts.toolUses(blocks)
			s.FilesTouched = appendFiles(s.FilesTouched, blocks, seen)
		case typeUser:
			ts.toolResults(blocks)
		}
	}
	s.Todos = ts.todos
}
