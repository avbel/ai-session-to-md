package antigravity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// store is a temp ~/.gemini with: a CLI conversation in the repo, one in
// another project, a subagent, an indexed conversation without a transcript
// and a desktop-app conversation that only has the truncated log.
type store struct {
	root, repo, other string
}

func newStore(t *testing.T, withIndex bool) store {
	t.Helper()
	base := t.TempDir()
	s := store{root: filepath.Join(base, ".gemini"), repo: filepath.Join(base, "repo"), other: filepath.Join(base, "other")}
	for _, d := range []string{s.repo, s.other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTranscript(t, s.root, cliApp, idMain, fullLog,
		userInput(0, "Fix the flaky login test"),
		planner(1, "", call("run_command", map[string]any{"CommandLine": "go test ./auth", "Cwd": s.repo})),
		result(2, "RUN_COMMAND", "DONE", "ok"),
		planner(3, "", call("replace_file_content", map[string]any{"TargetFile": filepath.Join(s.repo, "auth", "login.go")})),
		result(4, "CODE_ACTION", "DONE", "edited"),
		planner(5, "Fixed the race in login."),
	)
	writeTranscript(t, s.root, cliApp, idMain, truncatedLog, userInput(0, "TRUNCATED COPY"))
	writeFile(t, filepath.Join(s.root, cliApp, "brain", idMain, taskFile), "- `[x]` find the race\n- `[/]` add a regression test\n")
	writeTranscript(t, s.root, cliApp, idOther, fullLog, userInput(0, "Other project work"), planner(1, "done"))
	writeTranscript(t, s.root, cliApp, idChild, fullLog, userInput(0, "SUBAGENT ONLY"), planner(1, "done"))
	// The desktop app stores every tool argument JSON-encoded a second time.
	writeTranscript(t, s.root, desktopApp, idDesktop, truncatedLog,
		userInput(0, "Desktop app task"),
		planner(1, "", map[string]any{"name": "write_to_file", "args": map[string]any{
			"TargetFile": `"` + filepath.ToSlash(filepath.Join(s.repo, "main.go")) + `"`, "IsArtifact": "false",
		}}),
		result(2, "CODE_ACTION", "DONE", "created"),
		planner(3, "done"))
	if err := os.MkdirAll(filepath.Join(s.root, cliApp, "brain", idNoLog, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withIndex {
		writeIndex(t, s.root,
			sumRow{id: idMain, uris: `["` + fileURI(s.repo) + `"]`, modified: "2026-11-24 12:00:00.5+00:00", status: statusIdle, app: cliApp},
			sumRow{id: idOther, title: "Other work", uris: `["` + fileURI(s.other) + `"]`, modified: "2026-11-24 13:00:00+00:00", status: statusIdle, app: cliApp},
			sumRow{id: idChild, uris: `["` + fileURI(s.repo) + `"]`, modified: "2026-11-24 14:00:00+00:00", parent: idMain, depth: 1, app: cliApp},
			sumRow{id: idDesktop, uris: `["` + fileURI(s.other) + `","` + fileURI(s.repo) + `"]`, modified: "2026-11-24 11:00:00+00:00", status: statusIdle, app: desktopApp},
			sumRow{id: idNoLog, uris: `["` + fileURI(s.repo) + `"]`, modified: "2026-11-24 15:00:00+00:00", app: cliApp},
		)
	}
	// File mtimes older than the index times, so the index decides order.
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = filepath.Walk(s.root, func(p string, _ os.FileInfo, _ error) error {
		_ = os.Chtimes(p, old, old)
		return nil
	})
	return s
}

func ids(metas []session.Meta) []string {
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.ID
	}
	return out
}

func TestListScopeAndOrder(t *testing.T) {
	s := newStore(t, true)
	a := New(s.root)
	got, err := a.List(t.Context(), harness.ListOptions{Scope: harness.Scope{Root: s.repo}})
	if err != nil {
		t.Fatal(err)
	}
	// The child is a subagent and idNoLog has no transcript; the desktop
	// conversation matches through its second workspace.
	if want := []string{idMain, idDesktop}; len(got) != 2 || got[0].ID != want[0] || got[1].ID != want[1] {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
	m := got[0]
	if m.Harness != session.HarnessAntigravity || m.Cwd != s.repo || m.Title != "Fix the flaky login test" {
		t.Errorf("meta = %+v", m)
	}
	if want := time.Date(2026, 11, 24, 12, 0, 0, 5e8, time.UTC); !m.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want the index time %v", m.UpdatedAt, want)
	}
	if m.Source != filepath.Join(logsDir(s.root, cliApp, idMain), fullLog) {
		t.Errorf("Source = %q, want the full log", m.Source)
	}

	all, err := a.List(t.Context(), harness.ListOptions{Scope: harness.Scope{All: true}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{idOther, idMain}; len(all) != 2 || all[0].ID != want[0] || all[1].ID != want[1] {
		t.Fatalf("all ids = %v, want %v", ids(all), want)
	}
	if all[0].Title != "Other work" {
		t.Errorf("index title = %q", all[0].Title)
	}
}

func TestListTitleFromAnnotation(t *testing.T) {
	s := newStore(t, true)
	writeFile(t, filepath.Join(s.root, cliApp, "annotations", idOther+".pbtxt"), `title:"Generated title"`)
	got, err := New(s.root).List(t.Context(), harness.ListOptions{Scope: harness.Scope{Root: s.other}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != idOther || got[0].Title != "Generated title" {
		t.Fatalf("metas = %+v", got)
	}
}

func TestListWithoutIndex(t *testing.T) {
	s := newStore(t, false)
	writeFile(t, filepath.Join(s.root, cliApp, historyFile),
		`{"display":"Other project work","timestamp":1,"workspace":"`+filepath.ToSlash(s.other)+`","conversationId":"`+idOther+`"}`+"\n")
	a := New(s.root)
	got, err := a.List(t.Context(), harness.ListOptions{Scope: harness.Scope{Root: s.repo}})
	if err != nil {
		t.Fatal(err)
	}
	// Without the index the subagent cannot be told apart and lists; the
	// main conversation's cwd comes from its first command.
	found := map[string]session.Meta{}
	for _, m := range got {
		found[m.ID] = m
	}
	if m, ok := found[idMain]; !ok || m.Cwd != s.repo {
		t.Errorf("main = %+v (ok=%v), want cwd from the run_command Cwd", m, ok)
	}
	if _, ok := found[idOther]; ok {
		t.Error("other-project conversation listed in the repo scope")
	}
	other, err := a.List(t.Context(), harness.ListOptions{Scope: harness.Scope{Root: s.other}})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].ID != idOther || filepath.Clean(other[0].Cwd) != s.other {
		t.Errorf("other scope = %+v, want cwd from history.jsonl", other)
	}
}

func TestListUnreadableIndexWarns(t *testing.T) {
	s := newStore(t, false)
	writeFile(t, filepath.Join(s.root, cliApp, summariesFile), "not a sqlite database")
	var warns []string
	got, err := New(s.root, WithWarn(func(w string) { warns = append(warns, w) })).List(t.Context(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("transcripts must still list without the index")
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "subagent conversations are not filtered out") {
		t.Errorf("warns = %q", warns)
	}
}

func TestListMissingRoot(t *testing.T) {
	got, err := New(filepath.Join(t.TempDir(), "absent")).List(t.Context(), harness.ListOptions{Scope: harness.Scope{All: true}})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want empty, nil", got, err)
	}
}

func TestLoadDetailed(t *testing.T) {
	s := newStore(t, true)
	a := New(s.root)
	metas, err := a.List(t.Context(), harness.ListOptions{Scope: harness.Scope{Root: s.repo}})
	if err != nil || len(metas) == 0 {
		t.Fatalf("list: %v, %v", metas, err)
	}
	sess, info, err := a.LoadDetailed(t.Context(), metas[0])
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != idMain || sess.Model != "gemini-3.8-flash-high" || sess.Cwd != s.repo {
		t.Errorf("session = %+v", sess.Meta)
	}
	if len(sess.FilesTouched) != 1 || sess.FilesTouched[0] != filepath.Join(s.repo, "auth", "login.go") {
		t.Errorf("files = %q", sess.FilesTouched)
	}
	if len(sess.Todos) != 2 || sess.Todos[1].Status != "in_progress" {
		t.Errorf("todos = %+v", sess.Todos)
	}
	st := session.Derive(sess, info)
	if st.Text != "turn completed" || st.LastUser != "Fix the flaky login test" || st.LastAssistant != "Fixed the race in login." {
		t.Errorf("status = %+v", st)
	}
}

func TestLoadByIDOnly(t *testing.T) {
	s := newStore(t, true)
	sess, err := New(s.root).Load(t.Context(), session.Meta{ID: idDesktop})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Source != filepath.Join(logsDir(s.root, desktopApp, idDesktop), truncatedLog) || sess.Title != "Desktop app task" {
		t.Errorf("session = %+v", sess.Meta)
	}
	if len(sess.FilesTouched) != 1 || filepath.Clean(sess.FilesTouched[0]) != filepath.Join(s.repo, "main.go") {
		t.Errorf("files = %q, want the unwrapped TargetFile", sess.FilesTouched)
	}
}

func TestLoadMidTurnAndError(t *testing.T) {
	s := newStore(t, true)
	writeTranscript(t, s.root, cliApp, idOther, fullLog,
		userInput(0, "Explain the code"),
		rec(1, "SYSTEM", typeError, "DONE", map[string]any{"error": "HTTP 503: No capacity available"}),
	)
	a := New(s.root)
	sess, info, err := a.LoadDetailed(t.Context(), session.Meta{ID: idOther})
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Derive(sess, info).Text; got != "assistant turn ended with error" {
		t.Errorf("status = %q", got)
	}

	busyRoot := newStore(t, false).root
	writeIndex(t, busyRoot, sumRow{id: idMain, modified: "2026-11-24 12:00:00+00:00", status: "CASCADE_RUN_STATUS_RUNNING", app: cliApp, uris: `[]`})
	_, busy, err := New(busyRoot).LoadDetailed(t.Context(), session.Meta{ID: idMain})
	if err != nil {
		t.Fatal(err)
	}
	if !busy.MidTurn {
		t.Error("a running conversation is not reported mid-turn")
	}
}

func TestLoadRejectsBadID(t *testing.T) {
	a := New(t.TempDir())
	for _, id := range []string{"", "../../etc", idMain + "/x"} {
		if _, err := a.Load(t.Context(), session.Meta{ID: id}); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
}
