//go:build e2e

// Package e2e runs the real ai-session-to-md binary against synthetic harness
// stores and a fake OpenAI-compatible server. Everything here is black box:
// the tests only see argv, environment, files, stdout and stderr.
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// binaryPath is the binary built once in TestMain.
var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aismd-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create binary dir:", err)
		os.Exit(1)
	}
	name := "aismd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binaryPath = filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/ai-session-to-md")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build binary: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// repoRoot is the module root, two directories above this file.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ".."
	}
	return filepath.Dir(filepath.Dir(file))
}

// testEnv is one isolated run environment: a fake home, a fake harness store
// tree and a temporary git repository used as the working directory.
type testEnv struct {
	t    *testing.T
	home string
	repo string
	env  map[string]string
	llm  *fakeLLM
}

// newTestEnv creates the fake home and the git repo. Harness stores appear
// only when a fixture writer is called, so tests can also cover the empty
// case.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	requireGit(t)
	home := t.TempDir()
	e := &testEnv{t: t, home: home, repo: createRepo(t), env: map[string]string{}}
	e.env["HOME"] = home
	e.env["USERPROFILE"] = home
	e.env["CLAUDE_CONFIG_DIR"] = e.claudeDir()
	e.env["CODEX_HOME"] = e.codexDir()
	e.env["XDG_DATA_HOME"] = filepath.Join(home, ".local", "share")
	e.env["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
	e.env["APPDATA"] = filepath.Join(home, "AppData", "Roaming")
	e.env["LOCALAPPDATA"] = filepath.Join(home, "AppData", "Local")
	e.env["GIT_CONFIG_NOSYSTEM"] = "1"
	e.env["GIT_CONFIG_GLOBAL"] = filepath.Join(home, ".gitconfig")
	e.env["GIT_TERMINAL_PROMPT"] = "0"
	e.env["GIT_PAGER"] = "cat"
	e.env["NO_COLOR"] = "1"
	e.env["AISMD_BASE_URL"] = "http://127.0.0.1:1/v1"
	e.env["AISMD_MODEL"] = "test-model"
	e.env["AISMD_API_KEY"] = "test-key-fake"
	e.env["AISMD_TIMEOUT"] = "30s"
	return e
}

func (e *testEnv) claudeDir() string {
	return filepath.Join(e.home, ".claude")
}

func (e *testEnv) codexDir() string {
	return filepath.Join(e.home, ".codex")
}

func (e *testEnv) openCodeDir() string {
	return filepath.Join(e.home, ".local", "share", "opencode")
}

// set overrides one child environment value.
func (e *testEnv) set(key, value string) {
	e.env[key] = value
}

// unset removes one child environment value.
func (e *testEnv) unset(key string) {
	delete(e.env, key)
}

// useLLM starts a fake LLM server in the given mode and points the binary at
// it.
func (e *testEnv) useLLM(mode llmMode) *fakeLLM {
	e.llm = newFakeLLM(e.t, mode)
	e.env["AISMD_BASE_URL"] = e.llm.server.URL + "/v1"
	return e.llm
}

// droppedEnvKeys are inherited values that would break isolation or leak the
// developer's real configuration into the child.
var droppedEnvKeys = map[string]bool{
	"HOME": true, "USERPROFILE": true, "CLAUDE_CONFIG_DIR": true,
	"CODEX_HOME": true, "XDG_DATA_HOME": true, "XDG_CONFIG_HOME": true,
	"APPDATA": true, "LOCALAPPDATA": true, "OPENAI_API_KEY": true,
	"NO_COLOR": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true,
	"GIT_TERMINAL_PROMPT": true, "GIT_PAGER": true,
}

// childEnv is the process environment for one binary run: everything the OS
// needs to find git and its DLLs, minus the real user configuration, plus the
// isolated values.
func (e *testEnv) childEnv() []string {
	out := make([]string, 0, len(e.env)+32)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if droppedEnvKeys[key] || strings.HasPrefix(key, "AISMD_") {
			continue
		}
		out = append(out, kv)
	}
	for _, key := range slices.Sorted(maps.Keys(e.env)) {
		out = append(out, key+"="+e.env[key])
	}
	return out
}

// runResult is one captured binary invocation.
type runResult struct {
	Stdout string
	Stderr string
	Code   int
}

// run executes the binary in the temp repo with the isolated environment.
func (e *testEnv) run(args ...string) runResult {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.repo
	cmd.Env = e.childEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			e.t.Fatalf("run %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return runResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

// runOK runs the binary and requires exit code 0.
func (e *testEnv) runOK(args ...string) runResult {
	e.t.Helper()
	res := e.run(args...)
	if res.Code != 0 {
		e.t.Fatalf("run %v exited %d, want 0\nstderr:\n%s", args, res.Code, res.Stderr)
	}
	return res
}

// requireGit skips the test when git is not installed.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// createRepo builds a git repository with one commit, a remote and a dirty
// tracked file, so the handoff's repository section always has content.
func createRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.name", "E2E Bot")
	gitRun(t, dir, "config", "user.email", "e2e@example.com")
	writeFile(t, filepath.Join(dir, "pkg", "reduce", "reduce.go"), "package reduce\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial commit")
	gitRun(t, dir, "remote", "add", "origin", fakeRemoteURL)
	writeFile(t, filepath.Join(dir, "pkg", "reduce", "reduce.go"), "package reduce\n\nvar dirty = true\n")
	return dir
}

// gitRun runs one git command in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeFile creates the parent directories and writes content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
