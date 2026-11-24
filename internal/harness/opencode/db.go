package opencode

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/avbel/ai-session-to-md/internal/sqlitero"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

const detectSQL = `SELECT name FROM sqlite_master WHERE type='table' AND name IN ('session_v2','session_message')`

// store is an opened read-only database plus the v2-table verdict. Both
// session_v2 and session_message are required for the v2 path.
type store struct {
	db *sql.DB
	v2 bool
}

func (s *store) Close() {
	_ = s.db.Close()
}

// openRO opens the database read-only. immutable=1 ignores the WAL, which
// works when the file cannot be opened for the WAL read but may miss
// transactions written after the last checkpoint.
func openRO(path string, immutable bool) (*sql.DB, error) {
	uri := sqlitero.URI(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	if immutable {
		uri += "&immutable=1"
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", uri, err)
	}
	return db, nil
}

func openStoreAt(ctx context.Context, path string, immutable bool) (*store, error) {
	db, err := openRO(path, immutable)
	if err != nil {
		return nil, err
	}
	names, err := tableNames(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	return &store{db: db, v2: names["session_v2"] && names["session_message"]}, nil
}

func tableNames(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, detectSQL)
	if err != nil {
		return nil, fmt.Errorf("detect v2 tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		found[name] = true
	}
	return found, rows.Err()
}
