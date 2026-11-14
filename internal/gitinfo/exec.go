package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// gitPrefix hardens every git invocation. The session cwd comes from
// untrusted session data, and repo-local config can execute code, so pager,
// fsmonitor, external diff and hooks are all disabled up front.
var gitPrefix = []string{
	"--no-pager",
	"-c", "core.fsmonitor=false",
	"-c", "diff.external=",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.pager=cat",
}

// gitOut runs one hardened git command in dir with a 5s timeout and returns
// its stdout. No shell is involved; every argument is passed verbatim.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	argv := append(slices.Clone(gitPrefix), "-C", dir)
	argv = append(argv, args...)
	cmd := exec.CommandContext(cctx, "git", argv...)
	cmd.Env = gitEnv()

	out, err := cmd.Output()
	if err != nil {
		return "", gitError(args, dir, err)
	}
	return string(out), nil
}

// gitEnv keeps the inherited environment but stops git from prompting for
// credentials, spawning a pager, or taking index locks. GIT_OPTIONAL_LOCKS=0
// lets the concurrent status and diff reads run without an index.lock clash.
func gitEnv() []string {
	env := slices.DeleteFunc(slices.Clone(os.Environ()), func(e string) bool {
		return strings.HasPrefix(e, "GIT_TERMINAL_PROMPT=") ||
			strings.HasPrefix(e, "GIT_PAGER=") ||
			strings.HasPrefix(e, "GIT_OPTIONAL_LOCKS=")
	})
	return append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

func gitError(args []string, dir string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("git %s in %s: %w: %s", cmdName(args), dir, err, firstLine(exitErr.Stderr))
	}
	return fmt.Errorf("git %s in %s: %w", cmdName(args), dir, err)
}

func cmdName(args []string) string {
	if len(args) == 0 {
		return "(no subcommand)"
	}
	return args[0]
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

// credRe matches a scheme followed by user:password@, as `remote get-url`
// returns it for private remotes.
var credRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@]+:[^/@]*@`)

// stripCredentials removes user:password@ from a remote URL.
func stripCredentials(url string) string {
	return credRe.ReplaceAllString(strings.TrimSpace(url), "$1")
}
