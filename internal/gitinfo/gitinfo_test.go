package gitinfo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

const testRemoteURL = "https://github.com/avbel/demo.git"

// runGit runs a setup command in a temp repo with a pinned identity and an
// isolated HOME, so tests never depend on the machine's git config.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	writeFile(t, filepath.Join(dir, "a.txt"), "one\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func addCommit(t *testing.T, dir, msg string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), string(data)+msg+"\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", msg)
}

// setUpstream points origin at url and fabricates the remote-tracking ref
// refs/remotes/origin/main at ref ("" for no tracking ref), so @{u} resolves
// without any network access.
func setUpstream(t *testing.T, dir, url, ref string) {
	t.Helper()
	runGit(t, dir, "remote", "add", "origin", url)
	if ref == "" {
		return
	}
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", ref)
	runGit(t, dir, "config", "branch.main.merge", "refs/heads/main")
	runGit(t, dir, "config", "branch.main.remote", "origin")
}

// realRoot is what git reports as the toplevel: on macOS the temp dir is
// behind a symlink and git resolves it.
func realRoot(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestInfoLiveClean(t *testing.T) {
	dir := newRepo(t)
	rec := session.GitRecord{RepoURL: "https://github.com/avbel/fallback.git", Branch: "main", Commit: "000000000000"}

	res, err := Info(t.Context(), dir, rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceLive {
		t.Errorf("source = %q, want %q", res.Source, SourceLive)
	}
	if res.Root != realRoot(t, dir) {
		t.Errorf("root = %q, want %q", res.Root, realRoot(t, dir))
	}
	if res.Branch != "main" {
		t.Errorf("branch = %q, want main", res.Branch)
	}
	if len(res.Head) != 12 {
		t.Errorf("head = %q, want 12 chars", res.Head)
	}
	if res.HeadSubject != "initial commit" {
		t.Errorf("subject = %q, want %q", res.HeadSubject, "initial commit")
	}
	if res.RepoURL != rec.RepoURL {
		t.Errorf("repoURL = %q, want recorded fallback %q", res.RepoURL, rec.RepoURL)
	}
	if len(res.StatusLines) != 0 {
		t.Errorf("status = %v, want empty", res.StatusLines)
	}
	if res.DiffStatSummary != "" || len(res.DiffStatFiles) != 0 {
		t.Errorf("diffstat = %q %v, want empty", res.DiffStatSummary, res.DiffStatFiles)
	}
	if !res.UpstreamMissing {
		t.Error("no remote configured, want UpstreamMissing")
	}
	if res.Ahead != 1 {
		t.Errorf("ahead = %d, want 1 commit", res.Ahead)
	}
	if !slices.Contains(res.Warnings, "1 commits not pushed.") {
		t.Errorf("warnings = %v, want unpushed-commit warning", res.Warnings)
	}
}

func TestInfoDirtyTree(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "changed\n")
	writeFile(t, filepath.Join(dir, "untracked.txt"), "new\n")

	res, err := Info(t.Context(), dir, session.GitRecord{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StatusLines) != 2 {
		t.Errorf("status = %v, want 2 entries", res.StatusLines)
	}
	if !slices.Contains(res.Warnings, dirtyWarning) {
		t.Errorf("warnings = %v, want dirty warning", res.Warnings)
	}
	if !strings.Contains(res.DiffStatSummary, "1 file changed") {
		t.Errorf("diffstat summary = %q, want 1 file changed", res.DiffStatSummary)
	}
	if len(res.DiffStatFiles) != 1 || !strings.Contains(res.DiffStatFiles[0], "a.txt") {
		t.Errorf("diffstat files = %v, want a.txt", res.DiffStatFiles)
	}
}

func TestInfoAheadOfUpstream(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "second")
	addCommit(t, dir, "third")
	setUpstream(t, dir, testRemoteURL, "HEAD~2")

	res, err := Info(t.Context(), dir, session.GitRecord{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UpstreamMissing {
		t.Error("upstream configured, want UpstreamMissing false")
	}
	if res.Ahead != 2 || res.Behind != 0 {
		t.Errorf("ahead/behind = %d/%d, want 2/0", res.Ahead, res.Behind)
	}
	if !slices.Contains(res.Warnings, "2 commits not pushed.") {
		t.Errorf("warnings = %v, want unpushed warning", res.Warnings)
	}
}

func TestInfoBehindUpstream(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "second")
	addCommit(t, dir, "third")
	setUpstream(t, dir, testRemoteURL, "HEAD")
	runGit(t, dir, "reset", "--hard", "HEAD~1")

	res, err := Info(t.Context(), dir, session.GitRecord{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Behind != 1 || res.Ahead != 0 {
		t.Errorf("ahead/behind = %d/%d, want 0/1", res.Ahead, res.Behind)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", res.Warnings)
	}
}

func TestInfoDetachedHead(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "checkout", "--detach", "HEAD")

	res, err := Info(t.Context(), dir, session.GitRecord{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "HEAD" {
		t.Errorf("branch = %q, want HEAD for detached state", res.Branch)
	}
	if len(res.Head) != 12 {
		t.Errorf("head = %q, want 12 chars", res.Head)
	}
	warn := fmt.Sprintf("Current branch is %q; the session was recorded on %q.", "HEAD", "main")
	if !slices.Contains(res.Warnings, warn) {
		t.Errorf("warnings = %v, want branch mismatch warning %q", res.Warnings, warn)
	}
}

func TestInfoRecordedFallback(t *testing.T) {
	rec := session.GitRecord{RepoURL: "https://github.com/avbel/x", Branch: "feature", Commit: "abc123def456"}

	file := filepath.Join(t.TempDir(), "plain.txt")
	writeFile(t, file, "not a dir\n")
	cases := []struct {
		name string
		cwd  string
	}{
		{"missing dir", filepath.Join(t.TempDir(), "gone")},
		{"not a repo", t.TempDir()},
		{"a file", file},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Info(t.Context(), tc.cwd, rec)
			if err != nil {
				t.Fatal(err)
			}
			if res.Source != SourceRecorded {
				t.Errorf("source = %q, want %q", res.Source, SourceRecorded)
			}
			if res.Root != "" {
				t.Errorf("root = %q, want empty", res.Root)
			}
			if res.RepoURL != rec.RepoURL || res.Branch != rec.Branch || res.Head != rec.Commit {
				t.Errorf("result = %+v, want recorded values %+v", res, rec)
			}
			if len(res.StatusLines) != 0 || len(res.Warnings) != 0 {
				t.Errorf("status/warnings = %v/%v, want empty", res.StatusLines, res.Warnings)
			}
		})
	}
}

func TestInfoCredentialURLStripped(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://ci-user:hunter2@example.com/avbel/private.git")

	res, err := Info(t.Context(), dir, session.GitRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if res.RepoURL != "https://example.com/avbel/private.git" {
		t.Errorf("repoURL = %q, want credentials stripped", res.RepoURL)
	}
}

func TestInfoEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")

	res, err := Info(t.Context(), dir, session.GitRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceLive {
		t.Errorf("source = %q, want %q", res.Source, SourceLive)
	}
	if res.Root != realRoot(t, dir) {
		t.Errorf("root = %q, want %q", res.Root, realRoot(t, dir))
	}
	if res.Branch != "" || res.Head != "" || res.HeadSubject != "" {
		t.Errorf("branch/head/subject = %q/%q/%q, want empty", res.Branch, res.Head, res.HeadSubject)
	}
	if res.DiffStatSummary != "" || len(res.DiffStatFiles) != 0 {
		t.Errorf("diffstat = %q %v, want empty on repo without commits", res.DiffStatSummary, res.DiffStatFiles)
	}
	if !res.UpstreamMissing || res.Ahead != 0 {
		t.Errorf("upstream missing = %v, ahead = %d, want true/0", res.UpstreamMissing, res.Ahead)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", res.Warnings)
	}
}

func TestInfoStatusCapped(t *testing.T) {
	dir := newRepo(t)
	for i := range 250 {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), "x\n")
	}

	res, err := Info(t.Context(), dir, session.GitRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StatusLines) != maxStatusLines {
		t.Errorf("status lines = %d, want %d", len(res.StatusLines), maxStatusLines)
	}
	if !slices.Contains(res.Warnings, dirtyWarning) {
		t.Errorf("warnings = %v, want dirty warning", res.Warnings)
	}
}

func TestInfoDiffStatCapped(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	for i := range 60 {
		name := filepath.Join(dir, fmt.Sprintf("f%02d.txt", i))
		writeFile(t, name, "one\n")
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "sixty files")
	for i := range 60 {
		name := filepath.Join(dir, fmt.Sprintf("f%02d.txt", i))
		writeFile(t, name, "two\n")
	}

	res, err := Info(t.Context(), dir, session.GitRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DiffStatFiles) != maxDiffFiles {
		t.Errorf("diffstat files = %d, want %d", len(res.DiffStatFiles), maxDiffFiles)
	}
	if !strings.Contains(res.DiffStatSummary, "60 files changed") {
		t.Errorf("diffstat summary = %q, want 60 files changed", res.DiffStatSummary)
	}
}

func TestInfoContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Info(ctx, t.TempDir(), session.GitRecord{})
	if err == nil {
		t.Error("cancelled context, want error")
	}
}
