package codex

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// threadColumns must all exist in the threads table for the primary path to
// be usable; anything less means an unsupported Codex version → fallback.
var threadColumns = []string{
	"id", "rollout_path", "cwd", "title", "name", "updated_at_ms",
	"updated_at", "archived", "git_branch", "git_origin_url",
	"thread_source", "source",
}

// threadRow is one row of the threads table, normalized.
type threadRow struct {
	id           string
	rolloutPath  string
	cwd          string
	title        string
	name         string
	threadSource string
	source       string
	branch       string
	originURL    string
	updatedMs    int64
	archived     bool
}

// listed pairs a Meta with the harness-recorded origin URL, needed for the
// origin half of the scope rule; it is dropped once filtering is done.
type listed struct {
	meta   session.Meta
	origin string
}

// List returns the sessions, newest first, already filtered by scope and
// (unless included) without archived ones. Primary source is the newest
// state_*.sqlite; without a usable database it falls back to walking the
// rollout files.
func (a *Adapter) List(ctx context.Context, opt harness.ListOptions) ([]session.Meta, error) {
	lst, ok := a.listFromDB(ctx, opt)
	if !ok {
		var err error
		lst, err = a.listFromFiles()
		if err != nil {
			return nil, fmt.Errorf("list codex sessions: %w", err)
		}
	}

	lst = a.filterScope(lst, opt.Scope)
	lst = slices.DeleteFunc(lst, func(l listed) bool {
		return l.meta.Archived && !opt.IncludeArchived
	})

	sort.Slice(lst, func(i, j int) bool {
		if !lst[i].meta.UpdatedAt.Equal(lst[j].meta.UpdatedAt) {
			return lst[i].meta.UpdatedAt.After(lst[j].meta.UpdatedAt)
		}
		return lst[i].meta.ID < lst[j].meta.ID
	})
	if opt.Limit > 0 && len(lst) > opt.Limit {
		lst = lst[:opt.Limit]
	}

	out := make([]session.Meta, len(lst))
	for i, l := range lst {
		out[i] = l.meta
	}
	return out, nil
}

// listFromDB reads the threads table of the newest state database. ok=false
// means the primary path is unavailable (no database, open failure or schema
// guard failure) and the caller should use the filesystem fallback.
func (a *Adapter) listFromDB(ctx context.Context, opt harness.ListOptions) ([]listed, bool) {
	dbPath := newestStateDB(a.dir)
	if dbPath == "" {
		return nil, false
	}

	db, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		a.log("open codex %s: %v; using file fallback", dbPath, err)
		return nil, false
	}
	defer func() { _ = db.Close() }()

	rows, err := a.queryThreads(ctx, db)
	if err != nil {
		a.log("read codex %s: %v; using file fallback", dbPath, err)
		return nil, false
	}
	return a.buildMetas(ctx, dbPath, rows), true
}

// queryThreads runs the schema guard and the threads SELECT.
func (a *Adapter) queryThreads(ctx context.Context, db *sql.DB) ([]threadRow, error) {
	if err := guardThreadsTable(ctx, db); err != nil {
		return nil, err
	}

	const q = `SELECT id, rollout_path, cwd, title, COALESCE(name,''), updated_at_ms,
		updated_at, archived, COALESCE(git_branch,''), COALESCE(git_origin_url,''),
		COALESCE(thread_source,''), source
		FROM threads ORDER BY COALESCE(updated_at_ms, updated_at*1000) DESC`
	rs, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("select threads: %w", err)
	}
	defer func() { _ = rs.Close() }()

	var rows []threadRow
	for rs.Next() {
		r, err := scanThreadRow(rs)
		if err != nil {
			return nil, fmt.Errorf("scan thread row: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, rs.Err()
}

// guardThreadsTable verifies that threads exists with every column the
// SELECT names.
func guardThreadsTable(ctx context.Context, db *sql.DB) error {
	rs, err := db.QueryContext(ctx, `PRAGMA table_info(threads)`)
	if err != nil {
		return fmt.Errorf("pragma table_info(threads): %w", err)
	}
	defer func() { _ = rs.Close() }()

	have := map[string]bool{}
	for rs.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rs.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan table_info: %w", err)
		}
		have[name] = true
	}
	if err := rs.Err(); err != nil {
		return fmt.Errorf("read table_info: %w", err)
	}
	for _, c := range threadColumns {
		if !have[c] {
			return fmt.Errorf("threads table lacks column %s", c)
		}
	}
	return nil
}

func scanThreadRow(rs *sql.Rows) (threadRow, error) {
	var r threadRow
	var rollout, cwd, title, source sql.NullString
	var updMs, updS, archived sql.NullInt64
	err := rs.Scan(&r.id, &rollout, &cwd, &title, &r.name, &updMs, &updS,
		&archived, &r.branch, &r.originURL, &r.threadSource, &source)
	if err != nil {
		return r, err
	}
	r.rolloutPath = rollout.String
	r.cwd = cwd.String
	r.title = title.String
	r.source = source.String
	r.archived = archived.Int64 == 1
	r.updatedMs = coalesceMs(updMs, updS)
	return r, nil
}

// coalesceMs converts the updated_at_ms / updated_at pair to epoch millis.
func coalesceMs(ms, secs sql.NullInt64) int64 {
	switch {
	case ms.Valid && ms.Int64 > 0:
		return ms.Int64
	case secs.Valid && secs.Int64 > 0:
		return secs.Int64 * 1000
	default:
		return 0
	}
}

// buildMetas filters raw rows (subagents, spawn children, unloadable
// rollouts) and turns the rest into listed metas.
func (a *Adapter) buildMetas(ctx context.Context, dbPath string, rows []threadRow) []listed {
	spawned := childThreadIDs(ctx, dbPath, a.log)
	names := loadThreadNames(a.dir)

	out := make([]listed, 0, len(rows))
	for _, r := range rows {
		if !userThread(r) {
			a.log("drop codex thread %s: non-user source %q", r.id, nonUserSource(r))
			continue
		}
		if spawned[r.id] {
			a.log("drop codex thread %s: spawned child thread", r.id)
			continue
		}
		rollout := a.resolveRollout(r.rolloutPath, r.id)
		if rollout == "" {
			a.log("drop codex thread %s: rollout file not found (stored %q)", r.id, r.rolloutPath)
			continue
		}
		out = append(out, listed{
			meta:   a.metaFromRow(dbPath, r, names),
			origin: r.originURL,
		})
	}
	return out
}

// userThread applies the subagent filter: thread_source must be 'user', or
// empty with a plain (non-JSON) source value.
func userThread(r threadRow) bool {
	switch r.threadSource {
	case "user":
		return true
	case "":
		return !strings.HasPrefix(r.source, "{")
	default:
		return false
	}
}

// nonUserSource names the dropped row's source for the verbose log.
func nonUserSource(r threadRow) string {
	if r.threadSource != "" {
		return r.threadSource
	}
	return r.source
}

func (a *Adapter) metaFromRow(dbPath string, r threadRow, names map[string]string) session.Meta {
	return session.Meta{
		Harness:   session.HarnessCodex,
		ID:        r.id,
		Title:     threadTitle(r, names),
		Cwd:       r.cwd,
		Branch:    r.branch,
		UpdatedAt: time.UnixMilli(r.updatedMs).UTC(),
		Archived:  r.archived,
		Source:    fmt.Sprintf("db:%s#%s", dbPath, r.id),
	}
}

// threadTitle picks name, then the session_index thread_name, then the
// first line of the title column, truncated to 80 runes.
func threadTitle(r threadRow, names map[string]string) string {
	switch {
	case r.name != "":
		return truncateRunes(r.name, 80)
	case names[r.id] != "":
		return truncateRunes(names[r.id], 80)
	default:
		first, _, _ := strings.Cut(r.title, "\n")
		return truncateRunes(strings.TrimSpace(first), 80)
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
