package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/jsonl"
)

// readOnlyDSN builds a SQLite URI that opens the database read-only. Each
// path segment is percent-escaped; SQLite decodes the URI path itself.
// Windows paths get the /C:/... form the URI grammar needs.
func readOnlyDSN(path string) string {
	p := filepath.ToSlash(path)
	if vol := filepath.VolumeName(path); vol != "" {
		p = "/" + p
	}
	seg := strings.Split(p, "/")
	for i, s := range seg {
		seg[i] = url.PathEscape(s)
	}
	return "file:" + strings.Join(seg, "/") + "?mode=ro&_pragma=query_only(1)"
}

// newestStateDB returns the newest state_*.sqlite in dir by numeric suffix,
// or "" when none exists.
func newestStateDB(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "state_*.sqlite"))
	best, bestN := "", -1
	for _, m := range matches {
		n, ok := stateDBNumber(m)
		if ok && n > bestN {
			best, bestN = m, n
		}
	}
	return best
}

// stateDBNumber extracts the numeric suffix of state_N.sqlite.
func stateDBNumber(path string) (int, bool) {
	base := strings.TrimSuffix(filepath.Base(path), ".sqlite")
	_, num, ok := strings.Cut(base, "_")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(num)
	return n, err == nil && n >= 0
}

// childThreadIDs collects every child_thread_id in thread_spawn_edges. A
// missing table just means no children: nothing is dropped.
func childThreadIDs(ctx context.Context, dbPath string, log func(string, ...any)) map[string]bool {
	db, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		log("open codex %s for spawn edges: %v", dbPath, err)
		return nil
	}
	defer func() { _ = db.Close() }()

	rs, err := db.QueryContext(ctx, `SELECT child_thread_id FROM thread_spawn_edges`)
	if err != nil {
		log("read thread_spawn_edges: %v", err)
		return nil
	}
	defer func() { _ = rs.Close() }()

	out := map[string]bool{}
	for rs.Next() {
		var id sql.NullString
		if err := rs.Scan(&id); err != nil {
			log("scan thread_spawn_edges: %v", err)
			continue
		}
		if id.Valid && id.String != "" {
			out[id.String] = true
		}
	}
	return out
}

// loadThreadNames maps thread id → last thread_name from session_index.jsonl.
func loadThreadNames(dir string) map[string]string {
	out := map[string]string{}
	lines, closeFn, err := jsonl.Open(filepath.Join(dir, "session_index.jsonl"))
	if err != nil {
		return nil
	}
	defer closeFn()
	for _, line := range lines {
		var e struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if err := json.Unmarshal(line, &e); err != nil || e.ID == "" || e.ThreadName == "" {
			continue
		}
		out[e.ID] = e.ThreadName
	}
	return out
}

// resolveRollout finds an existing rollout file for a thread: the stored
// path re-rooted under the codex dir (preferred — a crafted database must not
// point reads outside the harness dir), then the stored path as recorded,
// then archived_sessions/<base>.zst, then the sessions glob by thread id.
func (a *Adapter) resolveRollout(stored, id string) string {
	if p := a.statCandidate(reRooted(stored, a.dir)); p != "" {
		return p
	}
	if p := a.statCandidate(stored); p != "" {
		return p
	}
	if p := a.statCandidate(archivedCandidate(stored, a.dir)); p != "" {
		return p
	}
	if id != "" {
		if p := globRollout(a.dir, id); p != "" {
			return p
		}
	}
	return ""
}

// statCandidate returns path when it exists as a regular file. Directories,
// FIFOs, sockets and devices are refused: a crafted rollout_path pointing at
// /dev/zero would hang or OOM the reader.
func (a *Adapter) statCandidate(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return path
}

// reRooted rebuilds a stored rollout path under the current codex dir by
// locating the sessions/ or archived_sessions/ marker segment.
func reRooted(stored, dir string) string {
	for _, marker := range []string{"archived_sessions/", "sessions/"} {
		if i := strings.Index(stored, marker); i >= 0 {
			return filepath.Join(dir, stored[i:])
		}
	}
	return ""
}

// archivedCandidate is the zst copy kept in archived_sessions.
func archivedCandidate(stored, dir string) string {
	base := filepath.Base(stored)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(base), ".zst") {
		base += ".zst"
	}
	return filepath.Join(dir, "archived_sessions", base)
}

// globRollout searches sessions/*/*/*/rollout-*-<id>.jsonl*; names embed a
// timestamp, so the lexicographically last match is the newest.
func globRollout(dir, id string) string {
	pattern := filepath.Join(dir, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl*")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return ""
	}
	slices.Sort(matches)
	return matches[len(matches)-1]
}
