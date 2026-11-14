package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/avbel/ai-session-to-md/internal/cli"
	"github.com/avbel/ai-session-to-md/internal/config"
	"github.com/avbel/ai-session-to-md/internal/handoff"
	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/harness/opencode"
	"github.com/avbel/ai-session-to-md/internal/llm"
	"github.com/avbel/ai-session-to-md/internal/session"
	"github.com/avbel/ai-session-to-md/internal/ui"
)

// Exit codes per the CLI spec.
const (
	exitOK         = 0
	exitRuntime    = 1
	exitUsage      = 2
	exitNoSessions = 3
	exitLLM        = 4
	exitCancelled  = 130
)

// errLLM wraps every failure of the summarize/validate phase so it maps to
// exit code 4; context cancellation still wins over it.
var errLLM = errors.New("llm step failed")

// maxDisplayTitle caps the CleanTitle cap for display rows.
const maxDisplayTitle = 200

// app bundles the process IO environment, injectable for tests.
type app struct {
	stdout  io.Writer
	stderr  io.Writer
	stdin   *os.File
	getenv  config.Env
	version string
}

// Run parses the arguments and runs one command, returning the exit code.
func (a *app) Run(args []string) int {
	flags, err := cli.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "ai-session-to-md: %v\n", err)
		return exitUsage
	}
	if flags.ShowVersion {
		_, _ = fmt.Fprintln(a.stdout, a.version)
		return exitOK
	}
	if flags.ShowHelp {
		cli.PrintUsage(a.stdout)
		return exitOK
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	u := ui.New(a.stderr, a.stdin, ui.DetectTTY(a.stdin), ui.DetectTTY(os.Stderr),
		ui.Options{Quiet: flags.Quiet, NoColor: flags.NoColor, Env: a.getenv})
	if err := a.run(ctx, u, flags); err != nil {
		if ctx.Err() != nil {
			// Ctrl-C interrupted a step: report as a cancellation with exit
			// 130 no matter how the step's own error was wrapped.
			u.Fail("cancelled")
			return exitCancelled
		}
		printError(u, err)
		return exitFor(err)
	}
	return exitOK
}

// exitFor maps an error to its exit code.
func exitFor(err error) int {
	var amb harness.ErrAmbiguous
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, context.Canceled), errors.Is(err, ui.ErrCancelled):
		return exitCancelled
	case errors.As(err, &amb):
		return exitUsage
	case errors.Is(err, harness.ErrNoSessions):
		return exitNoSessions
	case errors.Is(err, config.ErrUsage), errors.Is(err, cli.ErrUsage), errors.Is(err, llm.ErrUsage),
		errors.Is(err, harness.ErrInvalidPrefix):
		return exitUsage
	case errors.Is(err, errLLM):
		return exitLLM
	default:
		return exitRuntime
	}
}

func (a *app) run(ctx context.Context, u *ui.UI, flags *cli.Flags) error {
	log := a.verboseLog(flags.Verbose)

	cfg, err := config.Load(config.LoadOptions{Values: flags.Values(), Env: a.getenv, RequireModel: !flags.Raw})
	if err != nil {
		return err
	}
	if !flags.Raw {
		if err := requireAPIKey(cfg, a.getenv); err != nil {
			return err
		}
	}

	verbosef := func(format string, args ...any) { log(fmt.Sprintf(format, args...)) }
	dirs := resolveDirs(cfg, a.getenv)
	ads := buildAdapters(dirs, flags.Harness, u.Warn, verbosef)
	root, origin := scopeForCwd(ctx)
	scope := harness.Scope{All: flags.All, Root: root, OriginURL: origin}

	meta, err := a.selectSession(ctx, u, flags, cfg, log, ads, scope, root, dirs)
	if err != nil {
		return err
	}
	return a.runHandoff(ctx, u, flags, cfg, log, ads, meta)
}

// requireAPIKey fails early, before any picker or listing, when no key is
// configured and the endpoint is not on loopback. Loopback endpoints are
// allowed to run keyless.
func requireAPIKey(cfg *config.Config, getenv config.Env) error {
	if cfg.APIKey(getenv) != "" {
		return nil
	}
	if u, err := url.Parse(cfg.BaseURL); err == nil && llm.IsLoopbackHost(u.Hostname()) {
		return nil
	}
	msg := fmt.Sprintf("no API key in $%s", cfg.APIKeyEnv)
	if cfg.APIKeyEnv == "" {
		msg = "no API key in $AISMD_API_KEY or $OPENAI_API_KEY"
	}
	return &hintError{
		msg:   msg,
		hint:  "export the key, point --base-url at a loopback endpoint, or run with --raw",
		cause: config.ErrUsage,
	}
}

// verboseLog returns the --verbose diagnostics hook; silent when unset.
func (a *app) verboseLog(verbose bool) func(string) {
	if !verbose {
		return func(string) {}
	}
	h := slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	lg := slog.New(h)
	return func(msg string) { lg.Debug(msg) }
}

// selectSession finds the one session to summarize: by id prefix, --latest,
// or the interactive picker.
func (a *app) selectSession(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config,
	log func(string), ads []harness.Adapter, scope harness.Scope, root string, dirs config.Dirs) (session.Meta, error) {

	if flags.SessionID != "" {
		return a.resolveByID(ctx, u, flags, cfg, log, ads)
	}
	// The picker needs both terminals: stdin to drive it, stderr to draw it.
	if err := cli.PickDecision(false, flags.Latest, u.InTTY && u.ErrTTY); err != nil {
		return session.Meta{}, err
	}
	reg := buildRegistry(ads, u.Warn)
	metas, _, err := reg.List(ctx, harness.ListOptions{Scope: scope, IncludeArchived: flags.Archived, Limit: flags.Limit})
	if err != nil {
		if a.isFallbackStore(err) {
			return session.Meta{}, fmt.Errorf("%w: %v", cli.ErrUsage, err)
		}
		return session.Meta{}, err
	}
	if len(metas) == 0 {
		return session.Meta{}, noSessionsError(flags.All, root, dirs)
	}
	if flags.Latest {
		return metas[0], nil
	}
	return a.pick(ctx, u, cfg, metas, root, flags.All)
}

// noSessionsError builds the empty-list failure: with --all set it points at
// missing storage dirs, else at the wider scope.
func noSessionsError(all bool, root string, dirs config.Dirs) error {
	if all {
		h := &hintError{msg: "no sessions found in any project", cause: harness.ErrNoSessions}
		if hint := missingStorageHint(dirs); hint != "" {
			h.hint = hint
		}
		return h
	}
	return &hintError{
		msg:   fmt.Sprintf("no sessions for %s", ui.RelHome(root, homeDir())),
		hint:  "run with --all to search every project",
		cause: harness.ErrNoSessions,
	}
}

// missingStorageHint names the configured session-store directories that do
// not exist, so an --all run that finds nothing knows where it looked.
func missingStorageHint(dirs config.Dirs) string {
	var missing []string
	for _, d := range []struct{ flag, path string }{
		{"--claude-dir", dirs.Claude},
		{"--codex-dir", dirs.Codex},
		{"--opencode-dir", dirs.OpenCode},
	} {
		if info, err := os.Stat(d.path); err != nil || !info.IsDir() {
			missing = append(missing, d.path+" ("+d.flag+")")
		}
	}
	switch {
	case len(missing) == 0:
		return ""
	case len(missing) == 3:
		return "none of the session stores exist: " + strings.Join(missing, ", ")
	default:
		return "missing session stores: " + strings.Join(missing, ", ")
	}
}

// resolveByID matches the given session id prefix; in the OpenCode fallback
// store ids cannot be listed, so a direct load attempt decides.
func (a *app) resolveByID(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config,
	log func(string), ads []harness.Adapter) (session.Meta, error) {

	rd, err := newRedactor(cfg.RedactPatterns)
	if err != nil {
		return session.Meta{}, err
	}
	reg := buildRegistry(ads, u.Warn)
	meta, err := reg.Resolve(ctx, flags.SessionID)
	if err == nil {
		return meta, nil
	}
	if a.isFallbackStore(err) {
		// The OpenCode store has no v2 tables: return a stub meta whose Load
		// goes through session export.
		return session.Meta{ID: flags.SessionID, Title: flags.SessionID, Harness: session.HarnessOpenCode}, nil
	}
	if amb, ok := errors.AsType[harness.ErrAmbiguous](err); ok {
		a.printMatches(u, amb.Matches, rd)
		return session.Meta{}, err
	}
	if errors.Is(err, harness.ErrNoSessions) {
		return session.Meta{}, &hintError{
			msg:   fmt.Sprintf("no session matching %q", flags.SessionID),
			hint:  "run without a session id to pick from a list, or add --all to search every project",
			cause: err,
		}
	}
	return session.Meta{}, err
}

// isFallbackStore reports that a listing failed because the OpenCode store
// has no v2 tables: listing is impossible there and a session id must go
// through session export.
func (a *app) isFallbackStore(err error) bool {
	var ae *harness.AdapterError
	return errors.As(err, &ae) && ae.Harness == session.HarnessOpenCode &&
		errors.Is(ae.Err, opencode.ErrFallbackNeedsID)
}

// pick runs the interactive picker over redacted display copies of the metas
// and maps the picked row back to the original meta, so the redacted Cwd
// ("~/…") never reaches gitinfo.
func (a *app) pick(ctx context.Context, u *ui.UI, cfg *config.Config, metas []session.Meta, root string, all bool) (session.Meta, error) {
	disp, err := newRedactor(cfg.RedactPatterns)
	if err != nil {
		return session.Meta{}, err
	}
	shown := make([]session.Meta, len(metas))
	for i, m := range metas {
		shown[i] = m
		shown[i].Title = handoff.CleanTitle(disp.str(m.Title), maxDisplayTitle)
		shown[i].Cwd = disp.str(m.Cwd)
	}
	chosen, err := u.Pick(ctx, shown, root, all, time.Now())
	if err != nil {
		return session.Meta{}, err
	}
	for i, m := range metas {
		if m.ID == chosen.ID && m.Harness == chosen.Harness {
			return metas[i], nil
		}
	}
	return chosen, nil // unreachable: shown was built from metas
}

// printMatches renders the ambiguous-prefix table: picker-style columns over
// redacted, CleanTitle'd rows.
func (a *app) printMatches(u *ui.UI, metas []session.Meta, rd *redactor) {
	shown := make([]session.Meta, len(metas))
	for i, m := range metas {
		shown[i] = m
		shown[i].Title = handoff.CleanTitle(rd.str(m.Title), maxDisplayTitle)
	}
	_, _ = fmt.Fprintln(a.stderr, "matching sessions:")
	for _, row := range ui.FormatMatches(shown, time.Now(), u.Width()-2) {
		_, _ = fmt.Fprintln(a.stderr, "  "+rd.str(row))
	}
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
