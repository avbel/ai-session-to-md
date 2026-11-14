package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

const listSQL = `SELECT id, directory, COALESCE(title,''), time_updated, time_archived, COALESCE(model,''), version
FROM session_v2 WHERE parent_id IS NULL ORDER BY time_updated DESC`

func (a *Adapter) listV2(ctx context.Context, db *sql.DB, path string, opt harness.ListOptions) ([]session.Meta, error) {
	rows, err := db.QueryContext(ctx, listSQL)
	if err != nil {
		return nil, fmt.Errorf("list opencode sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.Meta
	for rows.Next() {
		m, err := scanMeta(rows, path)
		if err != nil {
			return nil, fmt.Errorf("scan opencode session row: %w", err)
		}
		if m.Archived && !opt.IncludeArchived {
			continue
		}
		if !scopeMatches(m.Cwd, opt.Scope) {
			continue
		}
		if m.Title == "" {
			// Keep the full first-user-message text: display truncation
			// happens in the CLI via handoff.CleanTitle, never at list time.
			m.Title = firstUserText(ctx, db, m.ID)
		}
		out = append(out, m)
		if opt.Limit > 0 && len(out) >= opt.Limit {
			break
		}
	}
	return out, rows.Err()
}

func scanMeta(rows *sql.Rows, path string) (session.Meta, error) {
	var (
		id, directory, title, model, version string
		updated, archived                    sql.NullInt64
	)
	if err := rows.Scan(&id, &directory, &title, &updated, &archived, &model, &version); err != nil {
		return session.Meta{}, err
	}
	// Model and version are resolved in Load; List keeps the exact planned
	// SELECT and ignores them.
	_, _ = model, version
	return session.Meta{
		Harness:   session.HarnessOpenCode,
		ID:        id,
		Title:     title,
		Cwd:       directory,
		UpdatedAt: msTime(updated.Int64),
		Archived:  archived.Valid,
		Source:    "db:" + path + "#" + id,
	}, nil
}

// firstUserText backs the empty-title fallback; any failure yields "" and the
// title stays empty rather than aborting the listing.
func firstUserText(ctx context.Context, db *sql.DB, id string) string {
	var data sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT data FROM session_message WHERE session_id=? AND type='user' ORDER BY seq LIMIT 1`, id,
	).Scan(&data)
	if err != nil {
		return ""
	}
	md, err := parseMsgData([]byte(data.String))
	if err != nil {
		return ""
	}
	return md.Text
}

// scopeMatches applies the §6 path rule: All, or cwd equal to the root, or
// cwd under root. OpenCode records no git origin, so the origin branch of the
// rule never applies here.
func scopeMatches(cwd string, sc harness.Scope) bool {
	if sc.All {
		return true
	}
	root := normPath(sc.Root)
	if root == "" {
		return true
	}
	c := normPath(cwd)
	return c == root || strings.HasPrefix(c, root+string(os.PathSeparator))
}

// normPath cleans and resolves symlinks, because macOS /tmp resolves to
// /private/tmp and sessions record either spelling.
func normPath(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// truncate cuts s to max bytes without splitting a UTF-8 rune.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
