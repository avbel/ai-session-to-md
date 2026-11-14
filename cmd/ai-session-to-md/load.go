package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/cli"
	"github.com/avbel/ai-session-to-md/internal/config"
	"github.com/avbel/ai-session-to-md/internal/handoff"
	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/harness/adapters"
	"github.com/avbel/ai-session-to-md/internal/harness/codex"
	"github.com/avbel/ai-session-to-md/internal/harness/opencode"
	"github.com/avbel/ai-session-to-md/internal/session"
	"github.com/avbel/ai-session-to-md/internal/ui"
)

// buildAdapters constructs the concrete adapters for the configured dirs and
// restricts them to the chosen harness, if any. A missing storage dir is
// skipped silently (DefaultAdapters).
func buildAdapters(dirs config.Dirs, chosen session.Harness, warn func(string), verbose func(format string, args ...any)) []harness.Adapter {
	all := adapters.DefaultAdapters(adapters.Dirs{
		Claude:   dirs.Claude,
		Codex:    dirs.Codex,
		OpenCode: dirs.OpenCode,
	}, warn, verbose)
	if chosen == "" {
		return all
	}
	var out []harness.Adapter
	for _, ad := range all {
		if ad.Name() == chosen {
			out = append(out, ad)
		}
	}
	return out
}

func buildRegistry(ads []harness.Adapter, warn func(string)) *harness.Registry {
	reg := harness.New(harness.WithWarn(warn))
	for _, ad := range ads {
		reg.Register(ad)
	}
	return reg
}

// resolveDirs applies the platform default storage dirs on top of the
// flag/config values, which config.Load already merged.
func resolveDirs(cfg *config.Config, getenv config.Env) config.Dirs {
	home, _ := os.UserHomeDir()
	d := config.Dirs{
		Claude: cmp.Or(cfg.Dirs.Claude, getenv("CLAUDE_CONFIG_DIR"), filepath.Join(home, ".claude")),
		Codex:  cmp.Or(cfg.Dirs.Codex, getenv("CODEX_HOME"), filepath.Join(home, ".codex")),
	}
	d.OpenCode = cmp.Or(cfg.Dirs.OpenCode, defaultOpenCodeDir(getenv, home))
	return d
}

// defaultOpenCodeDir pins $XDG_DATA_HOME/opencode when that env var is set,
// so test and custom environments never leak into the real store. Otherwise
// it tries ~/.local/share/opencode, then (on Windows) %LOCALAPPDATA%\opencode;
// the first that exists wins, else the first candidate.
func defaultOpenCodeDir(getenv config.Env, home string) string {
	if x := getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "opencode")
	}
	var candidates []string
	candidates = append(candidates, filepath.Join(home, ".local", "share", "opencode"))
	if runtime.GOOS == "windows" {
		if l := getenv("LOCALAPPDATA"); l != "" {
			candidates = append(candidates, filepath.Join(l, "opencode"))
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return candidates[0]
}

// scopeForCwd computes the picker scope: Root is the git toplevel of the
// current dir (else the dir itself), OriginURL the origin remote, if any.
func scopeForCwd(ctx context.Context) (root, origin string) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	root = cwd
	if r, ok := gitToplevel(ctx, cwd); ok {
		root = r
	}
	origin, _ = gitOriginURL(ctx, cwd)
	return root, origin
}

// gitToplevel runs git rev-parse --show-toplevel in dir with a short
// timeout; ok is false when dir is not inside a git repository.
func gitToplevel(ctx context.Context, dir string) (string, bool) {
	out, ok := gitOne(ctx, dir, "rev-parse", "--show-toplevel")
	return out, ok
}

// gitOriginURL runs git remote get-url origin; ok is false without a remote.
func gitOriginURL(ctx context.Context, dir string) (string, bool) {
	out, ok := gitOne(ctx, dir, "remote", "get-url", "origin")
	return out, ok
}

// gitOne runs one fixed-argument git command; the variable part is passed
// via cmd.Dir, never as an argument.
func gitOne(ctx context.Context, dir string, args ...string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	return s, s != ""
}

func openCodeAdapter(ads []harness.Adapter) *opencode.Adapter {
	for _, ad := range ads {
		if a, ok := ad.(*opencode.Adapter); ok {
			return a
		}
	}
	return nil
}

// loadSession loads the selected session with the harness-specific end
// signals: OpenCode via LoadDetailed, Codex via its StatusInfo hook, Claude
// Code with zero Info.
func loadSession(ctx context.Context, ads []harness.Adapter, meta session.Meta) (session.Session, session.Info, error) {
	for _, ad := range ads {
		if ad.Name() != meta.Harness {
			continue
		}
		switch a := ad.(type) {
		case *opencode.Adapter:
			res, err := a.LoadDetailed(ctx, meta)
			if err != nil {
				return session.Session{}, session.Info{}, fmt.Errorf("load opencode session: %w", err)
			}
			return res.Session, res.Info, nil
		case *codex.Adapter:
			s, err := a.Load(ctx, meta)
			if err != nil {
				return session.Session{}, session.Info{}, fmt.Errorf("load codex session: %w", err)
			}
			return s, a.StatusInfo(meta.ID), nil
		default:
			s, err := ad.Load(ctx, meta)
			if err != nil {
				return session.Session{}, session.Info{}, fmt.Errorf("load %s session: %w", meta.Harness, err)
			}
			return s, session.Info{}, nil
		}
	}
	return session.Session{}, session.Info{}, fmt.Errorf("harness %s storage is not available", meta.Harness)
}

// output is the resolved output destination.
type output struct {
	Path   string
	Dir    string
	Disp   string
	Stdout bool
}

// outputPlan resolves -o (or the default file name), checks the overwrite
// rules and asks for confirmation when needed.
func (a *app) outputPlan(ctx context.Context, u *ui.UI, flags *cli.Flags, h session.Harness) (output, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return output{}, fmt.Errorf("get working directory: %w", err)
	}
	if flags.Output == "-" {
		return output{Path: "-", Dir: cwd, Disp: "-", Stdout: true}, nil
	}
	p := flags.Output
	if p == "" {
		name, ok := handoff.OutputFileName(h)
		if !ok {
			return output{}, fmt.Errorf("no output file name for harness %q", h)
		}
		p = filepath.Join(cwd, name)
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, p)
	}
	dir := filepath.Dir(abs)
	if err := a.overwriteCheck(ctx, u, flags, abs); err != nil {
		return output{}, err
	}
	disp := p
	if dir == filepath.Clean(cwd) {
		disp = filepath.Base(abs)
	}
	return output{Path: p, Dir: dir, Disp: disp}, nil
}

// overwriteCheck applies the pure decision rules and, on a TTY, the confirm
// prompt for an existing file.
func (a *app) overwriteCheck(ctx context.Context, u *ui.UI, flags *cli.Flags, abs string) error {
	state := cli.OverwriteAbsent
	if info, err := os.Lstat(abs); err == nil { //nolint:gosec // G703: the output path is the user's own CLI argument, by design
		if info.Mode()&os.ModeSymlink != 0 {
			state = cli.OverwriteSymlink
		} else {
			state = cli.OverwriteExists
		}
	}
	if err := cli.OverwriteDecision(state, flags.Force, u.InTTY); err != nil {
		return err
	}
	if state == cli.OverwriteExists && !flags.Force {
		ok, err := u.Confirm(ctx, "Overwrite "+abs+"?")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("not overwritten: %w", ui.ErrCancelled)
		}
	}
	return nil
}
