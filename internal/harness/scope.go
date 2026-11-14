package harness

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// Matches reports whether a listed session belongs in the scope, judging
// only what Meta can carry: everything when s.All, else the cwd half of the
// rule. The origin half needs the raw origin each harness records, which
// never reaches Meta (its shape is frozen after wave 1), so adapters holding
// origin data combine PathMatches with SameOrigin themselves, as the Codex
// adapter does.
func Matches(m session.Meta, s Scope) bool {
	if s.All {
		return true
	}
	return PathMatches(m.Cwd, s.Root)
}

// PathMatches reports that cwd equals root or lives under it. Both paths are
// cleaned and resolved through symlinks first, because macOS /tmp maps to
// /private/tmp and a session recorded through one spelling must still match
// the other. Empty values never match.
func PathMatches(cwd, root string) bool {
	if cwd == "" || root == "" {
		return false
	}
	p, d := resolvePath(cwd), resolvePath(root)
	if p == d {
		return true
	}
	if d == string(os.PathSeparator) {
		return strings.HasPrefix(p, d)
	}
	return strings.HasPrefix(p, d+string(os.PathSeparator))
}

// SameOrigin reports that two recorded origin URLs point at the same
// repository, comparing their normalized forms. Empty values never match,
// so a harness that recorded no origin cannot be pulled in by OriginURL.
func SameOrigin(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return NormalizeOrigin(a) == NormalizeOrigin(b)
}

// NormalizeOrigin rewrites an origin into "host/path" form, so ssh,
// https and scp-style remotes of one repository collapse to the same value:
// the host is lowercased, credentials and a .git suffix are dropped, and
// git@host:x/y becomes host/x/y. Values without a parseable host are
// returned with the .git suffix and surrounding slashes stripped.
func NormalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if i := strings.LastIndex(origin, "@"); i >= 0 {
		rest := origin[i+1:]
		if before, after, ok := strings.Cut(rest, ":"); ok {
			origin = "ssh://" + origin[:i+1] + before + "/" + after
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return cleanOriginPath(origin)
	}
	return strings.ToLower(u.Host) + "/" + cleanOriginPath(u.Path)
}

func cleanOriginPath(p string) string {
	p = strings.Trim(p, "/")
	p, _ = strings.CutSuffix(p, ".git")
	return p
}

// resolvePath cleans a path and resolves its symlinks, ignoring errors
// because a recorded cwd may no longer exist on this machine.
func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}
