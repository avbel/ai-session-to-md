package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/jsonl"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// Adapter reads Claude Code sessions from <claude>/projects/*/*.jsonl.
// Create it with New; it opens every file read-only.
type Adapter struct {
	dir string
}

var _ harness.Adapter = (*Adapter)(nil)

// New returns a Claude Code adapter rooted at dir, the harness config dir
// (for example ~/.claude or $CLAUDE_CONFIG_DIR).
func New(dir string) *Adapter {
	return &Adapter{dir: dir}
}

// Name implements harness.Adapter.
func (a *Adapter) Name() session.Harness {
	return session.HarnessClaudeCode
}

type fileInfo struct {
	path  string
	id    string
	mtime time.Time
}

// List returns sessions sorted by UpdatedAt desc, filtered by scope, up to
// opt.Limit entries (0 means unlimited).
func (a *Adapter) List(ctx context.Context, opt harness.ListOptions) ([]session.Meta, error) {
	files, err := a.discover()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(x, y fileInfo) int {
		if c := y.mtime.Compare(x.mtime); c != 0 {
			return c
		}
		return strings.Compare(x.path, y.path)
	})
	var out []session.Meta
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list claude sessions: %w", err)
		}
		m, ok, err := a.describe(f, opt.Scope)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, m)
		if opt.Limit > 0 && len(out) >= opt.Limit {
			break
		}
	}
	return out, nil
}

// discover collects the top-level *.jsonl files of every project dir,
// ignoring subdirectories such as <id>/subagents.
func (a *Adapter) discover() ([]fileInfo, error) {
	projects := filepath.Join(a.dir, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read claude projects dir: %w", err)
	}
	var files []fileInfo
	for _, proj := range entries {
		if !proj.IsDir() {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(projects, proj.Name()))
		if err != nil {
			slogWarn("claude-code: skipping unreadable project dir", proj.Name(), err)
			continue
		}
		for _, e := range inner {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(projects, proj.Name(), e.Name())
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				path:  path,
				id:    strings.TrimSuffix(e.Name(), ".jsonl"),
				mtime: st.ModTime(),
			})
		}
	}
	return files, nil
}

const (
	markerCwd        = `"cwd"`
	markerCustom     = `"type":"custom-title"`
	markerAI         = `"type":"ai-title"`
	markerLastPrompt = `"type":"last-prompt"`
	markerUser       = `"type":"user"`
)

type metaInfo struct {
	cwd        string
	branch     string
	custom     string
	ai         string
	lastPrompt string
	firstUser  string
	users      int
}

// describe scans one file for metadata and reports whether it belongs in
// the list: it needs user entries and a scope match.
func (a *Adapter) describe(f fileInfo, scope harness.Scope) (session.Meta, bool, error) {
	mi, err := scanMeta(f.path)
	if err != nil {
		return session.Meta{}, false, fmt.Errorf("scan claude session %s: %w", f.path, err)
	}
	if mi.users == 0 || !inScope(mi.cwd, scope) {
		return session.Meta{}, false, nil
	}
	m := session.Meta{
		Harness:   session.HarnessClaudeCode,
		ID:        f.id,
		Title:     mi.title(),
		Cwd:       mi.cwd,
		Branch:    mi.branch,
		UpdatedAt: f.mtime,
		Source:    f.path,
	}
	return m, true, nil
}

// scanMeta streams the file, unmarshalling only lines that carry one of the
// metadata markers, and derives the list metadata.
func scanMeta(path string) (metaInfo, error) {
	seq, closeFn, err := jsonl.Open(path)
	if err != nil {
		return metaInfo{}, err
	}
	defer closeFn()
	var mi metaInfo
	for _, raw := range seq {
		if !hasMetaMarker(raw) {
			continue
		}
		var ln line
		if json.Unmarshal(raw, &ln) != nil {
			continue
		}
		mi.absorb(ln)
	}
	return mi, nil
}

func hasMetaMarker(raw []byte) bool {
	return bytes.Contains(raw, []byte(markerCwd)) ||
		bytes.Contains(raw, []byte(markerCustom)) ||
		bytes.Contains(raw, []byte(markerAI)) ||
		bytes.Contains(raw, []byte(markerLastPrompt)) ||
		bytes.Contains(raw, []byte(markerUser))
}

func (mi *metaInfo) absorb(ln line) {
	if ln.Cwd != "" && mi.cwd == "" {
		mi.cwd = ln.Cwd
	}
	if b := ln.GitBranch; b != "" && b != "HEAD" {
		mi.branch = b
	}
	if ln.CustomTitle != "" {
		mi.custom = ln.CustomTitle
	}
	if ln.AITitle != "" {
		mi.ai = ln.AITitle
	}
	if ln.LastPrompt != "" {
		mi.lastPrompt = ln.LastPrompt
	}
	if ln.Type == typeUser {
		mi.countUser(ln)
	}
}

// countUser increments the user count when the entry yields a UserText by
// the content rules; meta, compaction and tool-result entries do not count.
func (mi *metaInfo) countUser(ln line) {
	if ln.IsMeta || ln.IsCompactSummary {
		return
	}
	for _, t := range userTurns(ln) {
		if t.Kind == session.UserText {
			mi.users++
			if mi.firstUser == "" {
				mi.firstUser = t.Text
			}
			return
		}
	}
}

// title applies the precedence: custom, ai, last prompt, first user text.
func (mi *metaInfo) title() string {
	switch {
	case mi.custom != "":
		return mi.custom
	case mi.ai != "":
		return mi.ai
	case mi.lastPrompt != "":
		return mi.lastPrompt
	default:
		return truncateRunes(mi.firstUser, 80)
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// inScope reports that the session cwd equals the scope root or lives under
// it. Paths are resolved through symlinks because macOS /tmp maps to
// /private/tmp. Claude files record no git origin, so OriginURL is unused.
func inScope(cwd string, scope harness.Scope) bool {
	if scope.All {
		return true
	}
	if cwd == "" || scope.Root == "" {
		return false
	}
	c, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		c = filepath.Clean(cwd)
	}
	r, err := filepath.EvalSymlinks(scope.Root)
	if err != nil {
		r = filepath.Clean(scope.Root)
	}
	if c == r {
		return true
	}
	return strings.HasPrefix(c, r+string(os.PathSeparator))
}
