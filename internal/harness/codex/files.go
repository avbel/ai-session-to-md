package codex

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// listFromFiles is the fallback listing for Codex stores without a usable
// state database: it walks sessions/**/rollout-*.jsonl and
// archived_sessions/rollout-*.jsonl.zst, reading only each file's first
// session_meta line.
func (a *Adapter) listFromFiles() ([]listed, error) {
	var out []listed

	if err := a.walkSessionDir(filepath.Join(a.dir, "sessions"), &out); err != nil {
		return nil, err
	}
	if err := a.walkArchivedDir(filepath.Join(a.dir, "archived_sessions"), &out); err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].meta.UpdatedAt.After(out[j].meta.UpdatedAt)
	})
	return out, nil
}

// walkSessionDir collects rollout-*.jsonl files at any depth under sessions.
func (a *Adapter) walkSessionDir(dir string, out *[]listed) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isRolloutFile(d.Name()) {
			return nil
		}
		if l, ok := a.metaFromRollout(path, false); ok {
			*out = append(*out, l)
		}
		return nil
	})
}

// walkArchivedDir collects the zst rollouts stored flat in archived_sessions.
func (a *Adapter) walkArchivedDir(dir string, out *[]listed) error {
	matches, err := filepath.Glob(filepath.Join(dir, "rollout-*.jsonl.zst"))
	if err != nil {
		return fmt.Errorf("glob archived sessions: %w", err)
	}
	for _, path := range matches {
		if l, ok := a.metaFromRollout(path, true); ok {
			*out = append(*out, l)
		}
	}
	return nil
}

// isRolloutFile matches rollout-*.jsonl and rollout-*.jsonl.zst names.
func isRolloutFile(name string) bool {
	if !strings.HasPrefix(name, "rollout-") {
		return false
	}
	return strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst")
}

// metaFromRollout reads the first session_meta line and builds a Meta from
// it; UpdatedAt is the file mtime, like the plan's fallback listing.
func (a *Adapter) metaFromRollout(path string, archived bool) (listed, bool) {
	meta, ok := firstSessionMeta(a, path)
	if !ok || !userPayloadThread(meta) {
		if ok {
			a.log("drop codex rollout %s: non-user thread_source %q", path, meta.ThreadSource)
		}
		return listed{}, false
	}

	fi, err := os.Stat(path)
	if err != nil {
		return listed{}, false
	}
	id := meta.SessionID
	if id == "" {
		id = meta.ID
	}
	return listed{
		meta: session.Meta{
			Harness:   session.HarnessCodex,
			ID:        id,
			Cwd:       meta.Cwd,
			Branch:    meta.Git.Branch,
			UpdatedAt: fi.ModTime().UTC(),
			Archived:  archived,
			Source:    path,
		},
		origin: meta.Git.RepositoryURL,
	}, true
}

// firstSessionMeta parses the first session_meta line of a rollout file.
func firstSessionMeta(a *Adapter, path string) (metaPayload, bool) {
	lines, closeFn, err := openRollout(path)
	if err != nil {
		a.log("open codex rollout %s: %v", path, err)
		return metaPayload{}, false
	}
	defer closeFn()

	for _, line := range lines {
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil || env.Type != "session_meta" {
			continue
		}
		var meta metaPayload
		if err := json.Unmarshal(env.Payload, &meta); err != nil {
			a.log("parse session_meta in %s: %v", path, err)
			return metaPayload{}, false
		}
		return meta, true
	}
	return metaPayload{}, false
}
