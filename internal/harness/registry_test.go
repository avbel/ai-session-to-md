package harness

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

type fakeAdapter struct {
	name    session.Harness
	metas   []session.Meta
	delay   time.Duration
	err     error
	entered func()
	sawAll  bool
}

func (f *fakeAdapter) Name() session.Harness { return f.name }

func (f *fakeAdapter) List(ctx context.Context, _ ListOptions) ([]session.Meta, error) {
	if f.entered != nil {
		f.entered()
	}
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return f.metas, f.err
}

func (f *fakeAdapter) Load(_ context.Context, m session.Meta) (session.Session, error) {
	return session.Session{Meta: m}, nil
}

func meta(name session.Harness, id string, minutesAgo int) session.Meta {
	return session.Meta{
		Harness:   name,
		ID:        id,
		UpdatedAt: time.Now().Add(-time.Duration(minutesAgo) * time.Minute),
	}
}

func ids(metas []session.Meta) []string {
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.ID
	}
	return out
}

func TestListMergeOrdering(t *testing.T) {
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, metas: []session.Meta{
		meta(session.HarnessClaudeCode, "claude-1", 30),
		meta(session.HarnessClaudeCode, "claude-2", 5),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessCodex, metas: []session.Meta{
		meta(session.HarnessCodex, "codex-1", 60),
		meta(session.HarnessCodex, "codex-2", 4),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessOpenCode, metas: []session.Meta{
		meta(session.HarnessOpenCode, "open-1", 10),
	}})

	got, warns, err := reg.List(t.Context(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	wantIDs := []string{"codex-2", "claude-2", "open-1", "claude-1", "codex-1"}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, want)
		}
	}
}

func TestListLimit(t *testing.T) {
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessCodex, metas: []session.Meta{
		meta(session.HarnessCodex, "s-1", 50),
		meta(session.HarnessCodex, "s-2", 40),
		meta(session.HarnessCodex, "s-3", 30),
		meta(session.HarnessCodex, "s-4", 20),
		meta(session.HarnessCodex, "s-5", 10),
	}})

	got, _, err := reg.List(t.Context(), ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"s-5", "s-4"}
	if len(got) != len(want) {
		t.Fatalf("limit 2 should keep two entries, got %v", ids(got))
	}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, w)
		}
	}

	got, _, err = reg.List(t.Context(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantAll := []string{"s-5", "s-4", "s-3", "s-2", "s-1"}
	if len(got) != len(wantAll) {
		t.Fatalf("limit 0 means unlimited, got %v", ids(got))
	}
	for i, w := range wantAll {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, w)
		}
	}
}

func TestListBrokenAdapterWarns(t *testing.T) {
	var hooked []string
	reg := New(WithWarn(func(w string) { hooked = append(hooked, w) }))
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, metas: []session.Meta{
		meta(session.HarnessClaudeCode, "claude-1", 5),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessCodex, err: errors.New("disk on fire")})

	got, warns, err := reg.List(t.Context(), ListOptions{})
	if err != nil {
		t.Fatalf("one broken adapter must not fail the listing: %v", err)
	}
	if len(got) != 1 || got[0].ID != "claude-1" {
		t.Errorf("healthy adapter's sessions are kept, got %v", ids(got))
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "codex") || !strings.Contains(warns[0], "disk on fire") {
		t.Errorf("warning should name the harness and the error, got %v", warns)
	}
	if len(hooked) != 1 || !strings.Contains(hooked[0], "codex") {
		t.Errorf("warn hook should receive the warning, got %v", hooked)
	}
}

func TestListAllAdaptersFailed(t *testing.T) {
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, err: errors.New("a")})
	reg.Register(&fakeAdapter{name: session.HarnessCodex, err: errors.New("b")})

	_, warns, err := reg.List(t.Context(), ListOptions{})
	if err == nil {
		t.Fatal("every adapter failing must return an error")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("error should mention every failure, got %v", err)
	}
	if len(warns) != 2 {
		t.Errorf("warnings should still be collected, got %v", warns)
	}
}

func TestListEmptyRegistry(t *testing.T) {
	reg := New()
	got, warns, err := reg.List(t.Context(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 || len(warns) != 0 {
		t.Errorf("empty registry should list nothing, got %v / %v", got, warns)
	}
}

// concurrentBarrier makes each adapter hold inside its List call until all
// n are in; a caller that never observes the full count reports false, so a
// serial registry fails the test instead of hanging.
func concurrentBarrier(n int) func() bool {
	var mu sync.Mutex
	inside := 0
	return func() bool {
		mu.Lock()
		inside++
		mu.Unlock()
		deadline := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(deadline) {
			mu.Lock()
			all := inside == n
			mu.Unlock()
			if all {
				return true
			}
			time.Sleep(500 * time.Microsecond)
		}
		return false
	}
}

func TestListRunsAdaptersConcurrently(t *testing.T) {
	const n = 3
	barrier := concurrentBarrier(n)
	reg := New()
	fakes := make([]*fakeAdapter, n)
	for i := range n {
		f := &fakeAdapter{
			name: session.HarnessClaudeCode,
			metas: []session.Meta{
				meta(session.HarnessClaudeCode, "id-"+string(rune('a'+i)), i),
			},
		}
		f.entered = func() { f.sawAll = barrier() }
		fakes[i] = f
		reg.Register(f)
	}

	if _, _, err := reg.List(t.Context(), ListOptions{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	for i, f := range fakes {
		if !f.sawAll {
			t.Errorf("adapter %d never saw all %d adapters inside List; listing is not concurrent", i, n)
		}
	}
}

func TestListConcurrentTiming(t *testing.T) {
	reg := New()
	for _, h := range []session.Harness{session.HarnessClaudeCode, session.HarnessCodex, session.HarnessOpenCode} {
		reg.Register(&fakeAdapter{
			name:  h,
			delay: 40 * time.Millisecond,
			metas: []session.Meta{meta(h, string(h)+"-1", 5)},
		})
	}

	start := time.Now()
	_, _, err := reg.List(t.Context(), ListOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if elapsed >= 3*40*time.Millisecond {
		t.Errorf("listing should run the adapters concurrently, took %v", elapsed)
	}
}

func TestResolveUniqueMatch(t *testing.T) {
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, metas: []session.Meta{
		meta(session.HarnessClaudeCode, "abcdef01", 20),
		meta(session.HarnessClaudeCode, "abcdef02", 10),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessCodex, metas: []session.Meta{
		meta(session.HarnessCodex, "abcdef99", 5),
	}})

	got, err := reg.Resolve(t.Context(), "abcdef99")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "abcdef99" || got.Harness != session.HarnessCodex {
		t.Errorf("got %+v, want the codex session", got)
	}
}

func TestResolveIncludesArchived(t *testing.T) {
	archived := meta(session.HarnessCodex, "archived-x", 5)
	archived.Archived = true
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessCodex, metas: []session.Meta{archived}})

	got, err := reg.Resolve(t.Context(), "archived")
	if err != nil {
		t.Fatalf("archived sessions must be included in prefix matching: %v", err)
	}
	if got.ID != "archived-x" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, metas: []session.Meta{
		meta(session.HarnessClaudeCode, "abcdef01", 20),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessCodex, metas: []session.Meta{
		meta(session.HarnessCodex, "abcdef02", 5),
	}})

	_, err := reg.Resolve(t.Context(), "abcdef0")
	var amb ErrAmbiguous
	if !errors.As(err, &amb) {
		t.Fatalf("want ErrAmbiguous, got %v", err)
	}
	if len(amb.Matches) != 2 || amb.Prefix != "abcdef0" {
		t.Errorf("ambiguous error should carry both matches, got %+v", amb)
	}
	if amb.Matches[0].ID != "abcdef02" || amb.Matches[1].ID != "abcdef01" {
		t.Errorf("matches should be sorted newest first, got %v", ids(amb.Matches))
	}
}

func TestResolveNoMatch(t *testing.T) {
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessCodex, metas: []session.Meta{
		meta(session.HarnessCodex, "abcdef99", 5),
	}})

	_, err := reg.Resolve(t.Context(), "ffffff")
	if !errors.Is(err, ErrNoSessions) {
		t.Fatalf("want ErrNoSessions, got %v", err)
	}
}

func TestResolveRejectsBadPrefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"too short", "abcde"},
		{"empty", ""},
		{"leading dash", "-abcde"},
		{"invalid charset", "abc/de"},
		{"spaces", "abcdef g"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := New()
			_, err := reg.Resolve(t.Context(), tc.prefix)
			if !errors.Is(err, ErrInvalidPrefix) {
				t.Fatalf("want ErrInvalidPrefix, got %v", err)
			}
		})
	}
}

func TestResolveWarnsOnce(t *testing.T) {
	var hooked []string
	reg := New(WithWarn(func(w string) { hooked = append(hooked, w) }))
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, err: errors.New("a")})
	reg.Register(&fakeAdapter{name: session.HarnessCodex, err: errors.New("b")})

	_, err := reg.Resolve(t.Context(), "ffffff")
	if err == nil {
		t.Fatal("every adapter failing must error")
	}
	if len(hooked) != 2 {
		t.Fatalf("each failing adapter must warn exactly once, got %v", hooked)
	}
}

func TestResolveWarnsOnceWhenOtherAdaptersSucceed(t *testing.T) {
	var hooked []string
	reg := New(WithWarn(func(w string) { hooked = append(hooked, w) }))
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, metas: []session.Meta{
		meta(session.HarnessClaudeCode, "abcdef01", 5),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessOpenCode, err: errors.New("boom")})

	_, err := reg.Resolve(t.Context(), "ffffff")
	if !errors.Is(err, ErrNoSessions) {
		t.Fatalf("want ErrNoSessions, got %v", err)
	}
	if len(hooked) != 1 || !strings.Contains(hooked[0], "opencode") {
		t.Fatalf("the failing adapter must warn exactly once, got %v", hooked)
	}
}

// TestResolveAttachesAdapterErrors verifies the fallback-mode seam: the
// no-match error carries every per-adapter failure typed, so the CLI can
// detect e.g. opencode.ErrFallbackNeedsID without string matching.
func TestResolveAttachesAdapterErrors(t *testing.T) {
	sentinel := errors.New("fallback needs id")
	reg := New()
	reg.Register(&fakeAdapter{name: session.HarnessClaudeCode, metas: []session.Meta{
		meta(session.HarnessClaudeCode, "abcdef01", 5),
	}})
	reg.Register(&fakeAdapter{name: session.HarnessOpenCode, err: sentinel})

	_, err := reg.Resolve(t.Context(), "zzzzzz")
	if !errors.Is(err, ErrNoSessions) {
		t.Fatalf("want ErrNoSessions, got %v", err)
	}
	var ae *AdapterError
	if !errors.As(err, &ae) {
		t.Fatalf("want an AdapterError inside %v", err)
	}
	if ae.Harness != session.HarnessOpenCode || !errors.Is(ae.Err, sentinel) {
		t.Fatalf("AdapterError = %+v", ae)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("the sentinel must be reachable through the whole chain: %v", err)
	}
}

func TestValidatePrefixAcceptsValid(t *testing.T) {
	for _, p := range []string{"abcdef", "a-b_c-0", "session-id-with-dashes-123"} {
		if err := ValidatePrefix(p); err != nil {
			t.Errorf("ValidatePrefix(%q) = %v, want nil", p, err)
		}
	}
}
