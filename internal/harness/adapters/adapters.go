// Package adapters builds the concrete harness adapters for the configured
// storage directories. It is a separate package because the adapters depend
// on harness.Adapter, so package harness itself cannot import them without
// an import cycle; the CLI wiring constructs them here and registers them
// with harness.New.
package adapters

import (
	"os"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/harness/claudecode"
	"github.com/avbel/ai-session-to-md/internal/harness/codex"
	"github.com/avbel/ai-session-to-md/internal/harness/opencode"
)

// Dirs holds the per-harness storage directories, already expanded.
type Dirs struct {
	Claude, Codex, OpenCode string
}

// DefaultAdapters builds the three adapters for dirs. verbose receives
// per-item diagnostics (e.g. dropped Codex threads); warn receives genuine
// warnings the user should see (e.g. an OpenCode snapshot fallback). A
// harness whose directory does not exist is skipped silently, per the CLI
// rules; a directory that exists but fails to read surfaces later, as a
// warning from the adapter's List. Either hook may be nil.
func DefaultAdapters(dirs Dirs, warn func(string), verbose func(format string, args ...any)) []harness.Adapter {
	if warn == nil {
		warn = func(string) {}
	}
	if verbose == nil {
		verbose = func(string, ...any) {}
	}
	var out []harness.Adapter
	if isDir(dirs.Claude) {
		out = append(out, claudecode.New(dirs.Claude))
	}
	if isDir(dirs.Codex) {
		out = append(out, codex.New(dirs.Codex, codex.WithLogger(verbose)))
	}
	if isDir(dirs.OpenCode) {
		out = append(out, opencode.New(dirs.OpenCode, opencode.WithWarn(warn)))
	}
	return out
}

func isDir(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
