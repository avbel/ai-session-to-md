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
	metas, warns, err := r.fetchAll(ctx, opt)
	if err != nil {
		return nil, warns, err
	}
	sortNewestFirst(metas)
	return cutToLimit(metas, opt.Limit), warns, nil
}

// Resolve matches a validated session id prefix against every registered
// harness's session ids, with no scope, no limit and archived sessions
// included. Exactly one match is returned. Zero matches yield ErrNoSessions
// and several yield ErrAmbiguous carrying every hit, sorted newest first,
// for the caller to print.
func (r *Registry) Resolve(ctx context.Context, prefix string) (session.Meta, error) {
	if err := ValidatePrefix(prefix); err != nil {
		return session.Meta{}, err
	}
	metas, warns, err := r.fetchAll(ctx, ListOptions{Scope: Scope{All: true}, IncludeArchived: true})
	if err != nil {
		return session.Meta{}, err
	}
	for _, w := range warns {
		r.warn(w)
	}
	matches := metasWithPrefix(metas, prefix)
	switch len(matches) {
	case 0:
		return session.Meta{}, fmt.Errorf("no session matching %q: %w", prefix, ErrNoSessions)
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
// unsorted metas plus one warning per failing adapter. It errors only when
// every registered adapter failed, and never when none is registered.
func (r *Registry) fetchAll(ctx context.Context, opt ListOptions) ([]session.Meta, []string, error) {
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
	failed := 0
	var merged []session.Meta
	for i, o := range outcomes {
		if o.err != nil {
			failed++
			msg := fmt.Sprintf("listing %s failed: %v", r.adapters[i].Name(), o.err)
			warns = append(warns, msg)
			r.warn(msg)
			continue
		}
		merged = append(merged, o.metas...)
	}
	if failed == len(r.adapters) && len(r.adapters) > 0 {
		return nil, warns, fmt.Errorf("every harness listing failed: %s", strings.Join(warns, "; "))
	}
	return merged, warns, nil
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
