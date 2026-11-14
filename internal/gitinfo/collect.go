package gitinfo

import (
	"cmp"
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// fact holds the output of one git command.
type fact struct {
	out string
	err error
}

// Indices into the gathered facts array.
const (
	fURL = iota
	fBranch
	fHead
	fSubject
	fStatus
	fDiff
	fUpstream
	fCount
)

// gather runs the read-only git commands that only need the repository root,
// all in parallel; each carries its own timeout. status and diff --stat read
// worktree paths, so they are dropped when worktree is false (a local filter
// or diff driver is configured).
func gather(ctx context.Context, root string, worktree bool) [fCount]fact {
	specs := [fCount][]string{
		fURL:      {"remote", "get-url", "origin"},
		fBranch:   {"rev-parse", "--abbrev-ref", "HEAD"},
		fHead:     {"rev-parse", "--short=12", "HEAD"},
		fSubject:  {"log", "-1", "--format=%s"},
		fStatus:   {"status", "--porcelain=v1", "--untracked-files=normal"},
		fDiff:     {"diff", "--no-ext-diff", "--stat", "HEAD"},
		fUpstream: {"rev-list", "--left-right", "--count", "@{u}...HEAD"},
	}
	if !worktree {
		specs[fStatus] = nil
		specs[fDiff] = nil
	}
	var facts [fCount]fact
	var wg sync.WaitGroup
	for i := range specs {
		wg.Go(func() {
			if len(specs[i]) == 0 {
				return
			}
			facts[i].out, facts[i].err = gitOut(ctx, root, specs[i]...)
		})
	}
	wg.Wait()
	return facts
}

func live(ctx context.Context, root string, rec session.GitRecord) Result {
	facts := gather(ctx, root, !hasLocalDrivers(ctx, root))
	res := Result{
		Source:      SourceLive,
		Root:        root,
		RepoURL:     cmp.Or(stripCredentials(facts[fURL].out), stripCredentials(rec.RepoURL)),
		Branch:      strings.TrimSpace(facts[fBranch].out),
		Head:        strings.TrimSpace(facts[fHead].out),
		HeadSubject: strings.TrimSpace(facts[fSubject].out),
	}
	res.StatusLines = capLines(facts[fStatus].out, maxStatusLines)
	res.DiffStatSummary, res.DiffStatFiles = parseDiffStat(facts[fDiff].out)
	res.Ahead, res.Behind, res.UpstreamMissing = aheadBehind(facts[fUpstream])
	if res.UpstreamMissing {
		res.Ahead = countCommits(ctx, root)
	}
	res.Warnings = warnings(res, rec.Branch)
	return res
}

// aheadBehind parses `rev-list --left-right --count @{u}...HEAD`. The left
// side is the upstream, so its count is how far HEAD is behind. Any failure
// means there is no usable upstream.
func aheadBehind(f fact) (ahead, behind int, missing bool) {
	if f.err != nil {
		return 0, 0, true
	}
	left, right, ok := strings.Cut(strings.TrimSpace(f.out), "\t")
	if !ok {
		return 0, 0, true
	}
	b, errBehind := strconv.Atoi(left)
	a, errAhead := strconv.Atoi(right)
	if errBehind != nil || errAhead != nil {
		return 0, 0, true
	}
	return a, b, false
}

// countCommits counts commits on HEAD that no remote ref reaches, used as
// the unpushed estimate when the branch has no upstream. A repo with no
// commits, or with no remotes at all, counts 0 for the former and every
// commit for the latter — the warning wording says "exist only here" for
// that case.
func countCommits(ctx context.Context, root string) int {
	out, err := gitOut(ctx, root, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// parseDiffStat splits `diff --stat HEAD` output into the trailing summary
// line ("N files changed, …") and at most maxDiffFiles per-file lines. An
// empty repo has no HEAD to diff against, which parses as empty.
func parseDiffStat(out string) (summary string, files []string) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", nil
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "|") {
		summary = last
		lines = lines[:len(lines)-1]
	}
	return summary, lines[:min(len(lines), maxDiffFiles)]
}

func splitLines(out string) []string {
	var lines []string
	for l := range strings.Lines(out) {
		l = strings.TrimSuffix(l, "\n")
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func capLines(out string, max int) []string {
	lines := splitLines(out)
	return lines[:min(len(lines), max)]
}
