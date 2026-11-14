package opencode

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/proj/opencode.db", "file:///tmp/proj/opencode.db"},
		{`C:\Users\avbel\AppData\opencode.db`, "file:///C:/Users/avbel/AppData/opencode.db"},
		{`C:/Users/x/opencode.db`, "file:///C:/Users/x/opencode.db"},
		{"relative/opencode.db", "file:relative/opencode.db"},
	}
	for _, tc := range cases {
		if got := sqliteURI(tc.in); got != tc.want {
			t.Errorf("sqliteURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOpenStoreWarnsOnSnapshotRetry(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_snapsh1", "/proj", "s", "", "v1", 1763000100000, nil)
	_ = db.Close()

	a := New(filepath.Dir(path))
	var warns []string
	a.warn = func(msg string) { warns = append(warns, msg) }
	ctx := context.Background()
	a.opener = func(immutable bool) (*store, error) {
		if !immutable {
			return nil, errors.New("disk I/O error")
		}
		return openStoreAt(ctx, path, true)
	}

	metas, err := a.List(ctx, listOptAll())
	if err != nil {
		t.Fatalf("List after retry: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("List rows = %d, want 1", len(metas))
	}
	if len(warns) != 1 ||
		warns[0] != "reading snapshot: sessions written since the last checkpoint may be missing" {
		t.Fatalf("warns = %q", warns)
	}
}

func TestOpenStoreNoWarnOnSuccess(t *testing.T) {
	path, db := buildTestDB(t)
	insertSession(t, db, "ses_clean01", "/proj", "s", "", "v1", 1763000100000, nil)
	_ = db.Close()

	a := New(filepath.Dir(path))
	var warns []string
	a.warn = func(msg string) { warns = append(warns, msg) }

	if _, err := a.List(context.Background(), listOptAll()); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warns = %q, want none", warns)
	}
}

func TestOpenStoreRetryFailsToo(t *testing.T) {
	path, db := buildTestDB(t)
	_ = db.Close()

	a := New(filepath.Dir(path))
	a.opener = func(immutable bool) (*store, error) {
		return nil, errors.New("still broken")
	}
	_, err := a.List(context.Background(), listOptAll())
	if err == nil || !strings.Contains(err.Error(), "still broken") {
		t.Fatalf("err = %v, want wrapped retry failure", err)
	}
}
