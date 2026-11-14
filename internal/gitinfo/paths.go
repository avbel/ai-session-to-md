package gitinfo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Relativize makes session file paths presentable in the handoff: paths under
// root become root-relative, other absolute paths become home-relative
// ("~/…"), and anything else stays as recorded. Symlinked path prefixes are
// resolved too, so macOS's /tmp → /private/tmp does not break the comparison.
// The result is deduplicated, sorted and capped at 100 entries.
//
// It lives here, rather than in the caller, because the right base — the git
// root — is only known after Info; it depends on nothing beyond the standard
// library. Call it with the session's FilesTouched and Result.Root.
func Relativize(files []string, root string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return relativize(files, root, home)
}

func relativize(files []string, root, home string) []string {
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, f := range files {
		rel := relative(f, root, home)
		if rel == "" {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	slices.Sort(out)
	return out[:min(len(out), maxRelFiles)]
}

// base is a directory prefix that paths may be made relative to; the home
// prefix renders with a leading ~/.
type base struct {
	path string
	home bool
}

func (b base) rel(p string) (string, bool) {
	r, ok := under(b.path, p)
	if !ok {
		return "", false
	}
	if b.home {
		return "~/" + r, true
	}
	return r, true
}

// relative makes one path relative to root or home. Relative and empty paths
// pass through untouched, as do absolute paths under neither prefix.
func relative(f, root, home string) string {
	if f == "" {
		return ""
	}
	f = filepath.Clean(f)
	if !filepath.IsAbs(f) {
		return f
	}
	abs := eval(f)
	bases := []base{
		{eval(root), false},
		{root, false},
		{eval(home), true},
		{home, true},
	}
	for _, b := range bases {
		if rel, ok := b.rel(abs); ok {
			return rel
		}
		if rel, ok := b.rel(f); ok {
			return rel
		}
	}
	return f
}

// under reports p relative to base when p is strictly inside base.
func under(basePath, p string) (string, bool) {
	if basePath == "" || !filepath.IsAbs(basePath) || !filepath.IsAbs(p) {
		return "", false
	}
	rel, err := filepath.Rel(basePath, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// eval resolves symlinks when the path exists on disk; a non-existent path is
// returned unchanged so deleted files still relativize by the literal path.
func eval(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
