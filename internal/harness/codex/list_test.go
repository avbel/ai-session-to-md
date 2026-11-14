package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

func listOpts(includeArchived bool, limit int) harness.ListOptions {
	return harness.ListOptions{
		Scope:           harness.Scope{All: true},
		IncludeArchived: includeArchived,
		Limit:           limit,
	}
}

func TestListPrimaryFiltersAndOrders(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	got := listAll(t.Context(), a, false, 0)

	want := []string{idFull, idEmpty, idRert, idTitl, idGlob}
	if !slices.Equal(got, want) {
		t.Fatalf("List ids = %v, want %v", got, want)
	}
}

func TestListIncludeArchived(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	got := listAll(t.Context(), a, true, 0)

	want := []string{idArch, idFull, idEmpty, idRert, idTitl, idGlob}
	if !slices.Equal(got, want) {
		t.Fatalf("List ids with archived = %v, want %v", got, want)
	}

	metas, err := a.List(t.Context(), listOpts(true, 0))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(metas, func(m session.Meta) bool { return m.ID == idArch })
	if i < 0 || !metas[i].Archived {
		t.Fatalf("archived meta missing or not flagged: %+v", metas)
	}
}

func TestListLimit(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	got := listAll(t.Context(), a, false, 2)
	if !slices.Equal(got, []string{idFull, idEmpty}) {
		t.Fatalf("limited ids = %v, want first two", got)
	}
}

func TestListDropsUnloadableAndLogs(t *testing.T) {
	var logs []string
	a := testAdapter(codexFixtureDir, &logs)
	got := listAll(t.Context(), a, false, 0)
	if slices.Contains(got, idMiss) {
		t.Fatalf("missing-rollout thread %s listed", idMiss)
	}
	found := slices.ContainsFunc(logs, func(l string) bool {
		return strings.Contains(l, idMiss) && strings.Contains(l, "rollout file not found")
	})
	if !found {
		t.Fatalf("verbose log for %s not recorded; logs = %v", idMiss, logs)
	}
}

// TestStatCandidateRejectsNonRegular reproduces the crafted-database finding:
// a rollout_path pointing at a FIFO (or any non-regular file) must be
// refused, or the reader would hang or OOM on it.
func TestStatCandidateRejectsNonRegular(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mkfifo is unavailable on windows")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "rollout-evil.jsonl")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo not available: %v", err)
	}
	a := New(dir)
	if got := a.statCandidate(fifo); got != "" {
		t.Fatalf("FIFO must be refused, got %q", got)
	}
	regular := filepath.Join(dir, "rollout-good.jsonl")
	if err := os.WriteFile(regular, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := a.statCandidate(regular); got != regular {
		t.Fatalf("regular file must pass, got %q", got)
	}
	if got := a.statCandidate(dir); got != "" {
		t.Fatalf("directory must be refused, got %q", got)
	}
}

// TestResolveRolloutPrefersRerooted verifies the security order: when both
// the stored absolute path and the re-rooted path exist, the one under the
// codex dir wins, so a crafted store cannot redirect reads elsewhere.
func TestResolveRolloutPrefersRerooted(t *testing.T) {
	dir := t.TempDir()
	id := "rerootpref-x"
	if err := os.MkdirAll(filepath.Join(dir, "sessions", "2026", "01", "02"), 0o755); err != nil {
		t.Fatal(err)
	}
	rerooted := filepath.Join(dir, "sessions", "2026", "01", "02",
		"rollout-2026-01-02T10-00-00-"+id+".jsonl")
	if err := os.WriteFile(rerooted, []byte("re-rooted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stored path with the sessions marker that also exists on disk.
	other := t.TempDir()
	stored := filepath.Join(other, "sessions", "2026", "01", "02",
		"rollout-2026-01-02T10-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(stored), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stored, []byte("stored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(dir)
	if got := a.resolveRollout(stored, id); got != rerooted {
		t.Fatalf("resolveRollout = %q, want the re-rooted %q", got, rerooted)
	}
}

func TestListTitles(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	metas, err := a.List(t.Context(), listOpts(false, 0))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, m := range metas {
		byID[m.ID] = m.Title
	}
	want := map[string]string{
		idFull: "Full Thread",      // name column wins
		idRert: "Indexed Title",    // session_index thread_name
		idTitl: "First line title", // first line of title column
		idGlob: "Glob Title",       // fallback chain
	}
	for id, w := range want {
		if byID[id] != w {
			t.Errorf("title of %s = %q, want %q", id, byID[id], w)
		}
	}
}

func TestListScopeRootAndOrigin(t *testing.T) {
	a := testAdapter(codexFixtureDir, nil)
	ctx := t.Context()

	byRoot, err := a.List(ctx, harness.ListOptions{Scope: harness.Scope{Root: "/tmp/proj"}})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := []string{idFull, idEmpty, idRert, idTitl}
	if !slices.Equal(idsOf(byRoot), wantRoot) {
		t.Fatalf("root scope ids = %v, want %v", idsOf(byRoot), wantRoot)
	}

	byOrigin, err := a.List(ctx, harness.ListOptions{
		Scope: harness.Scope{Root: "/elsewhere", OriginURL: "https://github.com/example/two.git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(idsOf(byOrigin), []string{idGlob}) {
		t.Fatalf("origin scope matched %v, want only %s", idsOf(byOrigin), idGlob)
	}
}

func idsOf(metas []session.Meta) []string {
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.ID
	}
	return out
}

func TestListFallbackWithoutDB(t *testing.T) {
	dir := t.TempDir()
	f1 := "f1f1f1f1-file"
	f2 := "f2f2f2f2-sub"
	f3 := "f3f3f3f3-arch"
	f4 := "f4f4f4f4-nojs"
	writeFallbackRollouts(t, dir, f1, f2, f3, f4)

	a := testAdapter(dir, nil)
	got := listAll(t.Context(), a, false, 0)
	if !slices.Equal(got, []string{f4, f1}) {
		t.Fatalf("fallback ids = %v, want %v (subagent and archived dropped)", got, []string{f4, f1})
	}

	got = listAll(t.Context(), a, true, 0)
	if !slices.Equal(got, []string{f4, f3, f1}) {
		t.Fatalf("fallback ids with archived = %v, want %v", got, []string{f4, f3, f1})
	}

	metas, err := a.List(t.Context(), listOpts(true, 0))
	if err != nil {
		t.Fatal(err)
	}
	archived := metas[slices.IndexFunc(metas, func(m session.Meta) bool { return m.ID == f3 })]
	if !archived.Archived || !strings.HasSuffix(archived.Source, ".zst") {
		t.Fatalf("archived fallback meta wrong: %+v", archived)
	}
	if archived.Cwd != "/tmp/proj" || archived.Branch != "dev" {
		t.Fatalf("fallback meta fields wrong: %+v", archived)
	}
	if archived.UpdatedAt.IsZero() {
		t.Fatal("fallback UpdatedAt must be the file mtime")
	}
}

func TestLoadZstdRollout(t *testing.T) {
	dir := t.TempDir()
	id := "aaaa0000-zst"
	writeZstArchived(t, dir, id, "zst content")

	a := testAdapter(dir, nil)
	metas, err := a.List(t.Context(), listOpts(true, 0))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list zst fixture: %v metas=%v", err, metas)
	}
	s, err := a.Load(t.Context(), metas[0])
	if err != nil {
		t.Fatalf("load zst rollout: %v", err)
	}
	want := []session.TurnKind{session.UserText, session.AssistantText}
	if !slices.Equal(kindsOf(s.Turns), want) {
		t.Fatalf("zst turns = %v, want %v", kindsOf(s.Turns), want)
	}
	if s.Turns[0].Text != "zst content" || s.Model != "gpt-5.2" {
		t.Fatalf("zst content wrong: %+v", s.Turns)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://github.com/example/one.git", "ssh://git@github.com/example/one.git", true},
		{"git@github.com:example/one", "https://github.com/example/one", true},
		{"https://user:pass@GitHub.com/example/one.git/", "ssh://git@github.com/example/one", true},
		{"https://github.com/example/one.git", "https://github.com/example/one.git", true},
		{"https://github.com/example/one.git", "https://github.com/example/two.git", false},
		{"https://github.com/example/one.git", "https://gitlab.com/example/one.git", false},
	}
	for _, c := range cases {
		if got := normalizeOrigin(c.a) == normalizeOrigin(c.b); got != c.want {
			t.Errorf("normalizeOrigin(%q) == normalizeOrigin(%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
