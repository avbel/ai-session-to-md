package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/avbel/ai-session-to-md/internal/session"
)

const (
	loadSessionSQL = `SELECT directory, COALESCE(title,''), time_updated, time_archived, COALESCE(model,''), version
FROM session_v2 WHERE id=?`
	loadMessagesSQL = `SELECT type, time_created, data FROM session_message WHERE session_id=? ORDER BY seq`
	todoSQL         = `SELECT content, status FROM todo WHERE session_id=? ORDER BY position`
)

func (a *Adapter) loadV2(ctx context.Context, db *sql.DB, m session.Meta) (LoadResult, error) {
	base, modelRaw, version, err := scanSessionRow(ctx, db, a.dbPath(), m.ID)
	if err != nil {
		return LoadResult{}, err
	}
	b := newBuilder(base)
	b.model = modelJSON(modelRaw)
	b.ver = version

	if err := streamMessages(ctx, db, m.ID, b.addMsg); err != nil {
		return LoadResult{}, err
	}
	return LoadResult{Session: b.build(queryTodos(ctx, db, m.ID)), Info: b.info}, nil
}

func scanSessionRow(ctx context.Context, db *sql.DB, path, id string) (session.Meta, string, string, error) {
	var (
		directory, title, modelRaw, version string
		updated, archived                   sql.NullInt64
	)
	err := db.QueryRowContext(ctx, loadSessionSQL, id).
		Scan(&directory, &title, &updated, &archived, &modelRaw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Meta{}, "", "", fmt.Errorf("opencode session %s not found: %w", id, err)
	}
	if err != nil {
		return session.Meta{}, "", "", fmt.Errorf("load opencode session %s: %w", id, err)
	}
	base := session.Meta{
		Harness:   session.HarnessOpenCode,
		ID:        id,
		Title:     title,
		Cwd:       directory,
		UpdatedAt: msTime(updated.Int64),
		Archived:  archived.Valid,
		Source:    "db:" + path + "#" + id,
	}
	return base, modelRaw, version, nil
}

// streamMessages walks the session's messages in seq order. A malformed data
// payload or an unknown type counts toward Skipped instead of failing.
func streamMessages(ctx context.Context, db *sql.DB, id string, add func(msg)) error {
	rows, err := db.QueryContext(ctx, loadMessagesSQL, id)
	if err != nil {
		return fmt.Errorf("read opencode messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			typ     string
			created sql.NullInt64
			data    sql.NullString
		)
		if err := rows.Scan(&typ, &created, &data); err != nil {
			return fmt.Errorf("scan opencode message: %w", err)
		}
		md, err := parseMsgData([]byte(data.String))
		if err != nil {
			add(msg{})
			continue
		}
		add(msg{Type: typ, Data: md, Time: msTime(created.Int64)})
	}
	return rows.Err()
}

// queryTodos reads the todo table. A missing or empty table yields nil and
// the caller falls back to the last todowrite tool input.
func queryTodos(ctx context.Context, db *sql.DB, id string) []session.Todo {
	rows, err := db.QueryContext(ctx, todoSQL, id)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []session.Todo
	for rows.Next() {
		var t session.Todo
		if err := rows.Scan(&t.Content, &t.Status); err != nil {
			return out
		}
		out = append(out, session.Todo{Content: t.Content, Status: normTodoStatus(t.Status)})
	}
	return out
}
