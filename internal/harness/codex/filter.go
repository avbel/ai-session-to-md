package codex

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/avbel/ai-session-to-md/internal/harness"
)

// filterScope keeps the sessions the scope admits: everything, or sessions
// whose cwd is the root, lives under it, or was recorded against the same
// normalized origin (the recorded origin never reaches Meta, so the
// comparison happens here, with the raw value).
func (a *Adapter) filterScope(lst []listed, s harness.Scope) []listed {
	if s.All {
		return lst
	}
	out := lst[:0:0]
	for _, l := range lst {
		if scopeMatch(l, s.Root, s.OriginURL) {
			out = append(out, l)
		}
	}
	return out
}

func scopeMatch(l listed, root, originURL string) bool {
	if sameDir(l.meta.Cwd, root) || underDir(l.meta.Cwd, root) {
		return true
	}
	if originURL != "" && l.origin != "" {
		return normalizeOrigin(l.origin) == normalizeOrigin(originURL)
	}
	return false
}

// sameDir compares two paths after cleaning and resolving symlinks.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return bestEffortPath(a) == bestEffortPath(b)
}

// underDir reports whether path is a strict descendant of dir.
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	p, d := bestEffortPath(path), bestEffortPath(dir)
	if d == "/" {
		return p != "/" && strings.HasPrefix(p, "/")
	}
	return p != d && strings.HasPrefix(p, d+string(os.PathSeparator))
}

// bestEffortPath cleans and resolves symlinks, ignoring errors (the path
// may not exist locally).
func bestEffortPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// normalizeOrigin rewrites an origin into "host/path" form: scp-style
// git@host:x/y and scheme'd URLs all collapse to the same value, so an
// ssh clone matches an https clone of the same repo.
func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	if i := strings.Index(origin, "@"); i >= 0 {
		rest := origin[i+1:]
		if before, after, ok := strings.Cut(rest, ":"); ok {
			origin = "ssh://" + origin[:i+1] + before + "/" + after
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return cleanPathTail(origin)
	}
	return strings.ToLower(u.Host) + cleanPathTail(u.Path)
}

func cleanPathTail(p string) string {
	p = strings.Trim(p, "/")
	if s, ok := strings.CutSuffix(p, ".git"); ok {
		p = s
	}
	return p
}
