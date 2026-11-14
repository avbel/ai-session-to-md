package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/session"
)

func TestPathMatches(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	sub := filepath.Join(root, "sub")
	deep := filepath.Join(sub, "a", "b")
	sibling := filepath.Join(base, "repo-sibling")
	siblingPrefix := filepath.Join(base, "repoX")
	for _, d := range []string{deep, sibling, siblingPrefix} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		cwd  string
		root string
		want bool
	}{
		{"root itself", root, root, true},
		{"direct subdir", sub, root, true},
		{"deep subdir", deep, root, true},
		{"outside sibling", sibling, root, false},
		{"shared prefix without separator", siblingPrefix, root, false},
		{"empty cwd", "", root, false},
		{"empty root", sub, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PathMatches(tc.cwd, tc.root); got != tc.want {
				t.Errorf("PathMatches(%q, %q) = %v, want %v", tc.cwd, tc.root, got, tc.want)
			}
		})
	}
}

func TestPathMatchesSymlinkedRoot(t *testing.T) {
	realRoot := t.TempDir()
	sub := filepath.Join(realRoot, "project")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if !PathMatches(sub, link) {
		t.Errorf("subdir %q should match symlinked root %q", sub, link)
	}
	if !PathMatches(link, realRoot) {
		t.Errorf("symlink %q should match real root %q", link, realRoot)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	sameRepo := []string{
		"https://github.com/avbel/x.git",
		"https://GitHub.com/avbel/x.git",
		"https://github.com/avbel/x",
		"git@github.com:avbel/x.git",
		"ssh://git@github.com/avbel/x",
		"git://github.com/avbel/x.git",
		"https://user:pass@github.com/avbel/x",
	}
	for _, origin := range sameRepo {
		if got := NormalizeOrigin(origin); got != "github.com/avbel/x" {
			t.Errorf("NormalizeOrigin(%q) = %q, want %q", origin, got, "github.com/avbel/x")
		}
	}

	other := []string{
		"git@gitlab.com:avbel/x.git",
		"https://github.com/avbel/y",
		"https://github.com/avbel/x-extra",
	}
	for _, origin := range other {
		if got := NormalizeOrigin(origin); got == "github.com/avbel/x" {
			t.Errorf("NormalizeOrigin(%q) should not match the first repo", origin)
		}
	}

	if got := NormalizeOrigin("  "); got != "" {
		t.Errorf("NormalizeOrigin(blank) = %q, want empty", got)
	}
}

func TestSameOrigin(t *testing.T) {
	if !SameOrigin("git@github.com:avbel/x.git", "https://user:pass@github.com/avbel/x") {
		t.Error("ssh and credentialed https of one repo should match")
	}
	if SameOrigin("https://github.com/avbel/x", "https://github.com/avbel/y") {
		t.Error("different repos should not match")
	}
	if SameOrigin("", "https://github.com/avbel/x") {
		t.Error("empty recorded origin should never match")
	}
}

func TestMatches(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	in := session.Meta{Cwd: sub}
	out := session.Meta{Cwd: t.TempDir()}

	if !Matches(in, Scope{Root: root}) {
		t.Error("session under root should match")
	}
	if Matches(out, Scope{Root: root}) {
		t.Error("session outside root should not match")
	}
	if !Matches(out, Scope{All: true}) {
		t.Error("All scope should match any session")
	}
	if !Matches(session.Meta{}, Scope{All: true}) {
		t.Error("All scope should match a session without cwd")
	}
	if Matches(out, Scope{Root: root, OriginURL: "https://github.com/avbel/x"}) {
		t.Error("origin in the scope cannot pull in a Meta without a recorded origin")
	}
	if Matches(in, Scope{}) {
		t.Error("empty scope should match nothing")
	}
}
