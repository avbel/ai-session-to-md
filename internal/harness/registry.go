package harness

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/avbel/ai-session-to-md/internal/session"
)

var (
	// ErrNoSessions marks that a session id prefix matched nothing, mapped
	// to exit code 3 by the CLI.
	ErrNoSessions = errors.New("no sessions found")

	// ErrInvalidPrefix marks a session id argument that fails the syntax
	// rules, mapped to exit code 2.
	ErrInvalidPrefix = errors.New("invalid session id")
)

// ErrAmbiguous reports a session id prefix matching more than one session;
// the CLI prints Matches as a table and exits 2. Detect it with errors.As.
type ErrAmbiguous struct {
	Prefix  string
	Matches []session.Meta
}

func (e ErrAmbiguous) Error() string {
	return fmt.Sprintf("session id prefix %q is ambiguous: %d matches", e.Prefix, len(e.Matches))
}

// AdapterError is one harness adapter's listing failure, kept typed so the
// CLI can branch on the cause with errors.Is/As even when the other
// harnesses listed fine — e.g. OpenCode's fallback mode:
//
//	var ae *harness.AdapterError
//	if errors.As(err, &ae) && ae.Harness == session.HarnessOpenCode &&
//		errors.Is(ae.Err, opencode.ErrFallbackNeedsID) { …enter fallback… }
//
// Registry.Resolve attaches every AdapterError to its no-match error, and
// the all-adapters-failed error wraps them in an errors.Join.
type AdapterError struct {
	Harness session.Harness
	Err     error
}

func (e *AdapterError) Error() string {
	return fmt.Sprintf("listing %s failed: %v", e.Harness, e.Err)
}

func (e *AdapterError) Unwrap() error {
	return e.Err
}

var idPrefixRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)

// ValidatePrefix enforces the session id argument rules: at least six
// characters from [A-Za-z0-9_-], no leading dash.
func ValidatePrefix(prefix string) error {
	if strings.HasPrefix(prefix, "-") || !idPrefixRe.MatchString(prefix) {
		return fmt.Errorf("%w: %q must match [A-Za-z0-9_-]{6,} and must not start with '-'", ErrInvalidPrefix, prefix)
	}
	return nil
}

// Registry fans a listing or an id lookup out over the harness adapters.
// Register the adapters (see package adapters for DefaultAdapters) and call
// List or Resolve; warn receives the per-adapter warnings.
type Registry struct {
	warn     func(string)
	adapters []Adapter
}

// RegistryOption configures New.
type RegistryOption func(*Registry)

// WithWarn routes per-adapter warnings to fn; nil restores the silent
// default.
func WithWarn(fn func(string)) RegistryOption {
	return func(r *Registry) {
		if fn != nil {
			r.warn = fn
		}
	}
}

func New(opts ...RegistryOption) *Registry {
	r := &Registry{warn: func(string) {}}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Register adds an adapter; nil entries are ignored.
func (r *Registry) Register(a Adapter) {
	if a != nil {
		r.adapters = append(r.adapters, a)
	}
}

// List runs every registered adapter concurrently, merges the results into
// one list sorted by UpdatedAt desc and cuts it to opt.Limit (0 means
// unlimited). A failing adapter costs only a warning, so the other
// harnesses still contribute; when every adapter fails, the joined error is
// returned so a broken store does not read as "no sessions found".
func (r *Registry) List(ctx context.Context, opt ListOptions) ([]session.Meta, []string, error) {
	metas, warns, _, err := r.fetchAll(ctx, opt)
	if err != nil {
		return nil, warns, err
	}
	sortNewestFirst(metas)
	return cutToLimit(metas, opt.Limit), warns, nil
}

// Resolve matches a validated session id prefix against every registered
// harness's session ids, with no scope, no limit and archived sessions
// included. Exactly one match is returned. Zero matches yield ErrNoSessions
// (with every AdapterError attached, so a typed cause like
// opencode.ErrFallbackNeedsID stays inspectable) and several yield
// ErrAmbiguous carrying every hit, sorted newest first, for the caller to
// print. Per-adapter warnings are emitted exactly once, here via the warn
// hook.
func (r *Registry) Resolve(ctx context.Context, prefix string) (session.Meta, error) {
	if err := ValidatePrefix(prefix); err != nil {
		return session.Meta{}, err
	}
	metas, _, adapterErrs, err := r.fetchAll(ctx, ListOptions{Scope: Scope{All: true}, IncludeArchived: true})
	if err != nil {
		return session.Meta{}, err
	}
	matches := metasWithPrefix(metas, prefix)
	switch len(matches) {
	case 0:
		noMatch := fmt.Errorf("no session matching %q: %w", prefix, ErrNoSessions)
		if len(adapterErrs) == 0 {
			return session.Meta{}, noMatch
		}
		joined := make([]error, 0, len(adapterErrs)+1)
		joined = append(joined, noMatch)
		for _, ae := range adapterErrs {
			joined = append(joined, ae)
		}
		return session.Meta{}, errors.Join(joined...)
	case 1:
		return matches[0], nil
	default:
		sortNewestFirst(matches)
		return session.Meta{}, ErrAmbiguous{Prefix: prefix, Matches: matches}
	}
}

type fetchResult struct {
	metas []session.Meta
	err   error
}

// fetchAll queries every adapter concurrently and returns the unmerged,
// unsorted metas, one warning string per failing adapter (also emitted
// through the warn hook — the single warning site), the typed per-adapter
// errors for callers that need the causes, and an error only when every
// registered adapter failed (never when none is registered).
func (r *Registry) fetchAll(ctx context.Context, opt ListOptions) ([]session.Meta, []string, []*AdapterError, error) {
	outcomes := make([]fetchResult, len(r.adapters))
	var wg sync.WaitGroup
	for i, ad := range r.adapters {
		wg.Go(func() {
			metas, err := ad.List(ctx, opt)
			outcomes[i] = fetchResult{metas: metas, err: err}
		})
	}
	wg.Wait()

	warns := make([]string, 0, len(outcomes))
	var errs []*AdapterError
	failed := 0
	var merged []session.Meta
	for i, o := range outcomes {
		if o.err != nil {
			failed++
			ae := &AdapterError{Harness: r.adapters[i].Name(), Err: o.err}
			errs = append(errs, ae)
			warns = append(warns, ae.Error())
			r.warn(ae.Error())
			continue
		}
		merged = append(merged, o.metas...)
	}
	if failed == len(r.adapters) && len(r.adapters) > 0 {
		joined := make([]error, len(errs))
		for i, ae := range errs {
			joined[i] = ae
		}
		return nil, warns, errs, fmt.Errorf("every harness listing failed: %w", errors.Join(joined...))
	}
	return merged, warns, errs, nil
}

func metasWithPrefix(metas []session.Meta, prefix string) []session.Meta {
	var out []session.Meta
	for _, m := range metas {
		if strings.HasPrefix(m.ID, prefix) {
			out = append(out, m)
		}
	}
	return out
}

func sortNewestFirst(metas []session.Meta) {
	slices.SortFunc(metas, func(a, b session.Meta) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func cutToLimit(metas []session.Meta, limit int) []session.Meta {
	if limit > 0 && len(metas) > limit {
		return metas[:limit]
	}
	return metas
}
