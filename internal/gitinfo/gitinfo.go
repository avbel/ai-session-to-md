// Package gitinfo collects git facts about the directory an AI coding session
// ran in: repository root, remote URL, branch, HEAD, dirty state, diffstat and
// ahead/behind counts. It feeds the handoff's "Repository state" section and
// its warning block.
package gitinfo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// Source labels for Result.Source: whether the facts were measured in a live
// checkout, or come from what the harness recorded — which is all we have
// when the session's directory is missing on this machine or is not a
// repository.
const (
	SourceLive     = "live"
	SourceRecorded = "recorded by harness"
)

const (
	commandTimeout = 5 * time.Second
	maxStatusLines = 200
	maxDiffFiles   = 50
	maxRelFiles    = 100
)

// dirtyWarning is emitted whenever the tree has uncommitted changes, because
// the handoff file cannot carry them to the next machine.
const dirtyWarning = "Uncommitted changes are NOT in this file. Commit and push (e.g. a WIP branch) before switching machines."

type Result struct {
	Source          string
	Root            string
	RepoURL         string
	Branch          string
	Head            string
	HeadSubject     string
	StatusLines     []string
	DiffStatSummary string
	DiffStatFiles   []string
	Ahead           int
	Behind          int
	UpstreamMissing bool
	Warnings        []string
}

// Info measures the git state of the session's cwd.
//
// The fallback is the recorded values from the harness, never the current
// working directory: if cwd does not exist locally or is not a git
// repository, the result is labelled SourceRecorded and carries only the
// recorded RepoURL, Branch and Commit, because any other directory would
// report another project's state.
//
// rec is the session's RecordedGit. rec.Branch doubles as the branch the
// session recorded, used for the branch-mismatch warning.
//
// Git problems degrade instead of failing: not-a-repo becomes the recorded
// fallback, and a failed command leaves its field empty. Info returns an
// error only when the context is already cancelled.
func Info(ctx context.Context, cwd string, rec session.GitRecord) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("collect git info: %w", err)
	}
	dir, err := usableDir(cwd)
	if err != nil {
		return recorded(rec), nil
	}
	root, err := gitOut(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return recorded(rec), nil
	}
	return live(ctx, strings.TrimSpace(root), rec), nil
}

// usableDir returns cwd as an absolute, existing directory that can be passed
// to git -C, or an error when the session's directory is unusable on this
// machine.
func usableDir(cwd string) (string, error) {
	if cwd == "" {
		return "", fmt.Errorf("session has no cwd")
	}
	if !filepath.IsAbs(cwd) {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return "", fmt.Errorf("resolve session cwd: %w", err)
		}
		cwd = abs
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("session cwd %s: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("session cwd %s is not a directory", cwd)
	}
	return filepath.Clean(cwd), nil
}

func recorded(rec session.GitRecord) Result {
	return Result{
		Source:  SourceRecorded,
		RepoURL: stripCredentials(rec.RepoURL),
		Branch:  rec.Branch,
		Head:    rec.Commit,
	}
}

// warnings builds the handoff warning block: a dirty tree, unpushed commits,
// and a branch that differs from the one the session recorded. A missing
// upstream gets its own wording — those commits may be pushed to another
// branch's remote, or the branch may never have been pushed, so plain "not
// pushed" would be wrong.
func warnings(res Result, recordedBranch string) []string {
	var w []string
	if len(res.StatusLines) > 0 {
		w = append(w, dirtyWarning)
	}
	switch {
	case res.UpstreamMissing && res.Ahead > 0:
		w = append(w, fmt.Sprintf("branch %s has no upstream; %d commits exist only here — git push -u origin %s",
			res.Branch, res.Ahead, res.Branch))
	case res.UpstreamMissing && res.Branch != "":
		w = append(w, fmt.Sprintf("branch %s has no upstream.", res.Branch))
	case !res.UpstreamMissing && res.Ahead > 0:
		w = append(w, fmt.Sprintf("%d commits not pushed.", res.Ahead))
	}
	if recordedBranch != "" && res.Branch != "" && res.Branch != recordedBranch {
		w = append(w, fmt.Sprintf("Current branch is %q; the session was recorded on %q.", res.Branch, recordedBranch))
	}
	return w
}
