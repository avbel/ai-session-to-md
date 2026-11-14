package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/avbel/ai-session-to-md/internal/cli"
	"github.com/avbel/ai-session-to-md/internal/config"
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
	if err := a.run(ctx, flags); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "ai-session-to-md: %v\n", err)
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

func (a *app) run(ctx context.Context, flags *cli.Flags) error {
	log := a.verboseLog(flags.Verbose)
	u := ui.New(a.stderr, a.stdin, ui.DetectTTY(a.stdin), ui.DetectTTY(os.Stderr),
		ui.Options{Quiet: flags.Quiet, NoColor: flags.NoColor, Env: a.getenv})

	cfg, err := config.Load(config.LoadOptions{Values: flags.Values(), Env: a.getenv})
	if err != nil {
		return err
	}

	ads := buildAdapters(resolveDirs(cfg, a.getenv), flags.Harness, u.Warn)
	root, origin := scopeForCwd(ctx)
	scope := harness.Scope{All: flags.All, Root: root, OriginURL: origin}

	meta, err := a.selectSession(ctx, u, flags, cfg, log, ads, scope, root)
	if err != nil {
		return err
	}
	return a.runHandoff(ctx, u, flags, cfg, log, ads, meta)
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
	log func(string), ads []harness.Adapter, scope harness.Scope, root string) (session.Meta, error) {

	if flags.SessionID != "" {
		return a.resolveByID(ctx, u, flags, cfg, log, ads)
	}
	if err := cli.PickDecision(false, flags.Latest, u.InTTY); err != nil {
		return session.Meta{}, err
	}
	reg := buildRegistry(ads, u.Warn)
	metas, _, err := reg.List(ctx, harness.ListOptions{Scope: scope, IncludeArchived: flags.Archived, Limit: flags.Limit})
	if err != nil {
		if flags.Harness == session.HarnessOpenCode && isOpenCodeUnsupported(err) {
			return session.Meta{}, fmt.Errorf("%w: %v", cli.ErrUsage, err)
		}
		return session.Meta{}, err
	}
	if len(metas) == 0 {
		return session.Meta{}, fmt.Errorf("no sessions for %s; try --all: %w", root, harness.ErrNoSessions)
	}
	if flags.Latest {
		return metas[0], nil
	}
	return a.pick(ctx, u, cfg, metas, root)
}

// resolveByID matches the given session id prefix; in the OpenCode fallback
// store ids cannot be listed, so a direct load attempt decides.
func (a *app) resolveByID(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config,
	log func(string), ads []harness.Adapter) (session.Meta, error) {

	reg := buildRegistry(ads, u.Warn)
	meta, err := reg.Resolve(ctx, flags.SessionID)
	if err == nil {
		return meta, nil
	}
	if errors.Is(err, harness.ErrNoSessions) && flags.Harness == session.HarnessOpenCode {
		if oc := openCodeAdapter(ads); oc != nil && a.opencodeFallback(ctx, oc) {
			return session.Meta{ID: flags.SessionID, Title: flags.SessionID, Harness: session.HarnessOpenCode}, nil
		}
	}
	if amb, ok := errors.AsType[harness.ErrAmbiguous](err); ok {
		a.printMatches(amb.Matches)
	}
	return session.Meta{}, err
}

// opencodeFallback reports that the OpenCode store has no v2 tables, so
// listing is impossible and a session id must go through session export.
func (a *app) opencodeFallback(ctx context.Context, oc *opencode.Adapter) bool {
	_, err := oc.List(ctx, harness.ListOptions{Scope: harness.Scope{All: true}, IncludeArchived: true})
	return err != nil && isOpenCodeUnsupported(err)
}

// pick runs the interactive picker over home-dir-redacted copies of the
// metas.
func (a *app) pick(ctx context.Context, u *ui.UI, cfg *config.Config, metas []session.Meta, root string) (session.Meta, error) {
	disp, err := newRedactor(cfg.RedactPatterns)
	if err != nil {
		return session.Meta{}, err
	}
	shown := make([]session.Meta, len(metas))
	for i, m := range metas {
		shown[i] = m
		shown[i].Title = disp.str(m.Title)
		shown[i].Cwd = disp.str(m.Cwd)
	}
	return u.Pick(ctx, shown, root, time.Now())
}

func (a *app) printMatches(metas []session.Meta) {
	_, _ = fmt.Fprintln(a.stderr, "matching sessions:")
	for _, m := range metas {
		_, _ = fmt.Fprintf(a.stderr, "  %s  %-9s  %s  %s\n",
			m.ID, m.Harness, m.UpdatedAt.Format("2006-01-02 15:04"), m.Title)
	}
}

// isOpenCodeUnsupported matches the fallback-mode listing error, whose
// sentinel is unexported in the adapter package.
func isOpenCodeUnsupported(err error) bool {
	return strings.Contains(err.Error(), "OpenCode storage not recognised")
}
