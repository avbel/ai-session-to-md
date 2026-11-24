package antigravity

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// listed is a conversation found on disk, before scope filtering.
type listed struct {
	meta       session.Meta
	workspaces []string
}

// List returns the conversations that have a readable transcript, newest
// first. The brain dirs decide what exists (an indexed conversation without a
// transcript cannot be handed off); the index adds workspaces, titles and
// times, and subagent conversations are left out.
func (a *Adapter) List(ctx context.Context, opt harness.ListOptions) ([]session.Meta, error) {
	sums := a.readSummaries(ctx)
	var history map[string]string
	var found []listed
	seen := map[string]bool{}
	for _, app := range apps {
		ids, err := brainIDs(filepath.Join(a.root, app, "brain"))
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("list antigravity conversations: %w", err)
			}
			if seen[id] {
				continue
			}
			path := transcriptIn(logsDir(a.root, app, id))
			if path == "" {
				a.log("skip antigravity conversation %s: no transcript in %s", id, app)
				continue
			}
			seen[id] = true
			sum := sums[id]
			if sum.isChild() {
				a.log("skip antigravity conversation %s: subagent of %s", id, sum.parent)
				continue
			}
			if sums == nil && history == nil {
				history = a.readHistory()
			}
			found = append(found, a.describe(ctx, app, id, path, sum, history))
		}
	}
	var out []session.Meta
	for _, l := range found {
		if inScope(l.workspaces, opt.Scope) {
			out = append(out, l.meta)
		}
	}
	slices.SortStableFunc(out, func(x, y session.Meta) int { return y.UpdatedAt.Compare(x.UpdatedAt) })
	if opt.Limit > 0 && len(out) > opt.Limit {
		out = out[:opt.Limit]
	}
	return out, nil
}

// describe builds the list row. Reading the transcript is the last resort,
// for a title or cwd the index and annotations do not have.
func (a *Adapter) describe(ctx context.Context, app, id, path string, sum summary, history map[string]string) listed {
	m := session.Meta{
		Harness: session.HarnessAntigravity,
		ID:      id,
		Title:   cmp.Or(a.annotationTitle(app, id), sum.title),
		Source:  path,
	}
	workspaces := sum.workspaces
	if len(workspaces) == 0 && history[id] != "" {
		workspaces = []string{history[id]}
	}
	if m.Title == "" || len(workspaces) == 0 {
		title, cwd := scanHead(ctx, path, len(workspaces) == 0)
		m.Title = cmp.Or(m.Title, title)
		if len(workspaces) == 0 && cwd != "" {
			workspaces = []string{cwd}
		}
	}
	if len(workspaces) > 0 {
		m.Cwd = workspaces[0]
	}
	m.UpdatedAt = sum.updated
	if st, err := os.Stat(path); err == nil && st.ModTime().After(m.UpdatedAt) {
		m.UpdatedAt = st.ModTime()
	}
	return listed{meta: m, workspaces: workspaces}
}

// inScope matches any of the conversation's workspaces: an agy session can
// span several (--add-dir). Antigravity records no git origin, so only the
// path half of the scope rule applies.
func inScope(workspaces []string, s harness.Scope) bool {
	if s.All {
		return true
	}
	for _, ws := range workspaces {
		if harness.PathMatches(ws, s.Root) {
			return true
		}
	}
	return false
}

// brainIDs lists the conversation dirs of one app; a missing brain dir is an
// app that is not installed.
func brainIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read antigravity brain %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && idPattern.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}
