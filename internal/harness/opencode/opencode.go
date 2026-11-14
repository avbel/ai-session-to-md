// Package opencode reads OpenCode's local store (<dir>/opencode.db, v2
// schema) into normalized sessions, with an `opencode session export`
// fallback for older or unrecognized databases.
//
// The registry (internal/harness/registry.go, wave 3) should construct it as
//
//	ad := opencode.New(dir, opencode.WithWarn(warnFn))
//
// and store it as a harness.Adapter. In fallback mode List reports a usage
// error ("OpenCode storage not recognised; pass a session id"); the CLI then
// builds session.Meta{ID: id} itself and calls Load directly.
package opencode

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// idPattern guards the export fallback exec. The CLI validates the session id
// too; the adapter re-checks so the exec is safe on its own.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)

// ErrFallbackNeedsID marks an OpenCode store without the v2 tables: listing
// is impossible and a session id must go through `opencode session export`.
// The CLI detects it on Resolve's no-match error via
// errors.As(err, *harness.AdapterError) + errors.Is(ae.Err, ErrFallbackNeedsID)
// and enters fallback mode with Meta{ID: <given id>}.
var ErrFallbackNeedsID = errors.New("OpenCode storage not recognised; pass a session id")

// Adapter implements harness.Adapter for OpenCode.
type Adapter struct {
	dir  string
	bin  string
	warn func(string)

	// opener is nil in production; openStore then opens the real database.
	// Tests replace it to simulate a read-only/WAL failure and the
	// immutable=1 retry.
	opener func(immutable bool) (*store, error)
}

// Option configures the adapter.
type Option func(*Adapter)

// WithWarn routes non-fatal warnings, such as snapshot reads after a
// read-only open failure, to warn instead of discarding them. The CLI wires
// its stderr/slog output here.
func WithWarn(warn func(string)) Option {
	return func(a *Adapter) {
		a.warn = warn
	}
}

// WithCommand overrides the opencode executable used by the export fallback.
func WithCommand(bin string) Option {
	return func(a *Adapter) {
		a.bin = bin
	}
}

// New builds an adapter reading <dir>/opencode.db. The directory is only
// ever opened read-only.
func New(dir string, opts ...Option) *Adapter {
	a := &Adapter{
		dir:  dir,
		bin:  "opencode",
		warn: func(string) {},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Adapter) Name() session.Harness {
	return session.HarnessOpenCode
}

func (a *Adapter) dbPath() string {
	return filepath.Join(a.dir, "opencode.db")
}

func (a *Adapter) List(ctx context.Context, opt harness.ListOptions) ([]session.Meta, error) {
	st, err := a.openStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("open opencode store: %w", err)
	}
	defer st.Close()
	if !st.v2 {
		return nil, ErrFallbackNeedsID
	}
	return a.listV2(ctx, st.db, a.dbPath(), opt)
}

func (a *Adapter) Load(ctx context.Context, m session.Meta) (session.Session, error) {
	res, err := a.LoadDetailed(ctx, m)
	if err != nil {
		return session.Session{}, err
	}
	return res.Session, nil
}

// LoadResult bundles the normalized session with the harness-specific end
// signals that session.Derive needs to name the stop reason.
type LoadResult struct {
	Session session.Session
	Info    session.Info
}

// LoadDetailed is Load plus session.Info. Callers holding only the
// harness.Adapter interface get Load and lose the finish state, so the CLI
// wiring should prefer LoadDetailed for OpenCode (as for Codex).
func (a *Adapter) LoadDetailed(ctx context.Context, m session.Meta) (LoadResult, error) {
	st, err := a.openStore(ctx)
	if err != nil {
		return LoadResult{}, fmt.Errorf("open opencode store: %w", err)
	}
	defer st.Close()
	if !st.v2 {
		return a.loadExport(ctx, m)
	}
	return a.loadV2(ctx, st.db, m)
}

// openStore opens the database and detects the v2 tables. If the read-only
// open or the first query fails, it retries once with immutable=1, which
// ignores the WAL, and warns that the result is a checkpoint snapshot.
func (a *Adapter) openStore(ctx context.Context) (*store, error) {
	open := a.opener
	if open == nil {
		path := a.dbPath()
		open = func(immutable bool) (*store, error) {
			return openStoreAt(ctx, path, immutable)
		}
	}
	st, err := open(false)
	if err == nil {
		return st, nil
	}
	st, err = open(true)
	if err != nil {
		return nil, fmt.Errorf("open opencode db %s: %w", a.dbPath(), err)
	}
	a.warn("reading snapshot: sessions written since the last checkpoint may be missing")
	return st, nil
}

func validSessionID(id string) bool {
	return idPattern.MatchString(id) && !strings.HasPrefix(id, "-")
}
