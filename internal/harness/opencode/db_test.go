package opencode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSQLiteURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/proj/opencode.db", "file:///tmp/proj/opencode.db"},
		{`C:\Users\avbel\AppData\opencode.db`, "file:///C:/Users/avbel/AppData/opencode.db"},
		{`C:/Users/x/opencode.db`, "file:///C:/Users/x/opencode.db"},
		{"relative/opencode.db", "file:relative/opencode.db"},
		{"/tmp/store#x/opencode.db", "file:///tmp/store%23x/opencode.db"},
		{"/tmp/store?x/opencode.db", "file:///tmp/store%3Fx/opencode.db"},
		{"/tmp/sp ace/opencode.db", "file:///tmp/sp%20ace/opencode.db"},
	}
	for _, tc := range cases {
		if got := sqliteURI(tc.in); got != tc.want {
			t.Errorf("sqliteURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestOpenStoreSpecialCharsPath reproduces the finding: a directory whose
// name contains # or ? must still open read-only; unescaped, the # silently
// truncated the URI and dropped mode=ro.
func TestOpenStoreSpecialCharsPath(t *testing.T) {
	base := t.TempDir()
	clean := filepath.Join(base, "cleandir")
	if err := os.MkdirAll(clean, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(clean, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	for _, ddl := range v2Schema() {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("exec %q: %v", ddl, err)
		}
	}
	insertSession(t, db, "ses_hash01", "/proj", "s", "", "v1", 1763000100000, nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// A plain-path DSN truncates at '?', so build the db under a clean name
	// and move it into the special-character directory afterwards. '?' is
	// illegal in a Windows file name, so there the target keeps only '#':
	// the '#' escaping is still exercised end to end, and TestSQLiteURI
	// covers '?' on every OS.
	dir := filepath.Join(base, "store#x?y")
	if runtime.GOOS == "windows" {
		dir = filepath.Join(base, "store#x")
	}
	if err := os.Rename(clean, dir); err != nil {
		t.Fatal(err)
	}

	a := New(dir)
	metas, err := a.List(context.Background(), listOptAll())
	if err != nil {
		t.Fatalf("List with #/? in dir: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "ses_hash01" {
		t.Fatalf("metas = %+v", metas)
	}
	// mode=ro must survive the escaping: a write through the read-only URI
	// must be refused.
	ro, err := sql.Open("sqlite", sqliteURI(filepath.Join(dir, "opencode.db"))+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if _, err := ro.Exec(`CREATE TABLE ro_check (x)`); err == nil {
		t.Error("the read-only URI must reject writes")
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
