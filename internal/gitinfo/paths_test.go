package gitinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func writeFileAll(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, "content\n")
	}
}

func TestRelativizeUnderRootAndHome(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")

	files := []string{
		filepath.Join(root, "src", "a.go"),
		filepath.Join(root, "src", "a.go"),
		filepath.Join(root, "b.go"),
		filepath.Join(home, "notes.md"),
		filepath.Join(home, "other", "c.go"),
		filepath.Join(t.TempDir(), "elsewhere.go"),
		"relative/d.go",
		"",
	}
	got := relativize(files, root, home)
	// elsewhere.go keeps its absolute path, which differs per run; check it
	// separately and compare the rest against the sorted remainder.
	abs := got[0]
	rest := got[1:]
	if !filepath.IsAbs(abs) {
		t.Errorf("path outside root and home = %q, want unchanged absolute", abs)
	}
	wantRest := []string{
		"b.go",
		filepath.Join("relative", "d.go"),
		filepath.Join("src", "a.go"),
		filepath.Join("~", "notes.md"),
		filepath.Join("~", "other", "c.go"),
	}
	if !slices.Equal(rest, wantRest) {
		t.Errorf("relativize = %v, want %v", rest, wantRest)
	}
}

func TestRelativizeExistingPaths(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	writeFileAll(t,
		filepath.Join(root, "src", "a.go"),
		filepath.Join(root, "b.go"),
	)

	got := relativize([]string{
		filepath.Join(root, "src", "a.go"),
		filepath.Join(root, "b.go"),
		filepath.Join(root, "missing.go"),
	}, root, "")
	want := []string{"b.go", "missing.go", filepath.Join("src", "a.go")}
	if !slices.Equal(got, want) {
		t.Errorf("relativize = %v, want %v", got, want)
	}
}

func TestRelativizeCapped(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	files := make([]string, 0, 150)
	for i := range 150 {
		files = append(files, filepath.Join(root, "d", fmt.Sprintf("f%03d.go", i)))
	}
	got := relativize(files, root, home)
	if len(got) != maxRelFiles {
		t.Errorf("relativize kept %d paths, want %d", len(got), maxRelFiles)
	}
	if !slices.IsSorted(got) {
		t.Error("relativize result not sorted")
	}
}

func TestRelativizeEmpty(t *testing.T) {
	if got := relativize(nil, "", ""); len(got) != 0 {
		t.Errorf("relativize(nil) = %v, want empty", got)
	}
}

func TestRelativizeExported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := Relativize([]string{filepath.Join(home, "f.go")}, "")
	if len(got) != 1 || got[0] != filepath.Join("~", "f.go") {
		t.Errorf("Relativize = %v, want [%s]", got, filepath.Join("~", "f.go"))
	}
}

// TestRelativizeHomeSlashLiteral pins the POSIX spelling of the home fold:
// "~/" plus forward-slash segments. On Windows the fold is native ("~\…"),
// so this literal-output test only runs on Unix.
func TestRelativizeHomeSlashLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the home fold uses the native separator on Windows")
	}
	got := relativize([]string{"/home/u/other/c.go"}, "", "/home/u")
	if len(got) != 1 || got[0] != "~/other/c.go" {
		t.Errorf("relativize = %v, want [~/other/c.go]", got)
	}
}

func TestStripCredentials(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://user:pass@github.com/x/y.git", "https://github.com/x/y.git"},
		{"https://user:@github.com/x/y.git", "https://github.com/x/y.git"},
		{"https://github.com/x/y.git", "https://github.com/x/y.git"},
		{"git@github.com:x/y.git", "git@github.com:x/y.git"},
		{"ssh://user@host/x", "ssh://host/x"},
		{"https://" + "a1b2c3d4e5f6a1b2c3d4" + "a1b2c3d4e5f6a1b2c3d4" + "@host/x", "https://host/x"},
		{"https://u:pa" + "@ss" + "@h/x", "https://h/x"},
		{"https://u:pa" + "/ss" + "@h/x", "https://h/x"},
		{"ssh://git@host:2222/x/y.git", "ssh://host:2222/x/y.git"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripCredentials(tc.in); got != tc.want {
			t.Errorf("stripCredentials(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
