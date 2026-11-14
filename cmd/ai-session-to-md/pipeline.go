package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/avbel/ai-session-to-md/internal/cli"
	"github.com/avbel/ai-session-to-md/internal/config"
	"github.com/avbel/ai-session-to-md/internal/gitinfo"
	"github.com/avbel/ai-session-to-md/internal/handoff"
	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/llm"
	"github.com/avbel/ai-session-to-md/internal/redact"
	"github.com/avbel/ai-session-to-md/internal/reduce"
	"github.com/avbel/ai-session-to-md/internal/session"
	"github.com/avbel/ai-session-to-md/internal/ui"
)

// redactor wraps the secret redactor and counts every redaction for the
// final status line.
type redactor struct {
	r     *redact.Redactor
	total atomic.Int64
}

func newRedactor(patterns []string) (*redactor, error) {
	r, err := redact.New(redact.WithPatterns(patterns))
	if err != nil {
		return nil, fmt.Errorf("build redactor: %w", err)
	}
	return &redactor{r: r}, nil
}

// str redacts one string and counts the matches.
func (rd *redactor) str(s string) string {
	res := rd.r.Apply(s)
	rd.total.Add(int64(res.Total()))
	return res.Text
}

// fn is the plain func form, used for validate's secret check and the LLM
// client's error redaction.
func (rd *redactor) fn() func(string) string {
	return rd.str
}

// runHandoff drives load → git → redact → reduce → summarize → write.
func (a *app) runHandoff(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config,
	log func(string), ads []harness.Adapter, meta session.Meta) error {

	rd, err := newRedactor(cfg.RedactPatterns)
	if err != nil {
		return err
	}
	out, err := a.outputPlan(ctx, u, flags, meta.Harness)
	if err != nil {
		return err
	}

	s, info, err := a.loadWithSpinner(ctx, u, ads, meta)
	if err != nil {
		return err
	}
	git, err := a.gitWithSpinner(ctx, u, meta.Cwd, s.RecordedGit)
	if err != nil {
		return err
	}
	sRed := redactWithSpinner(u, rd, s)
	status := session.Derive(sRed, info)

	sel, err := handoff.Select(out.Dir, meta.ID, log)
	if err != nil {
		return fmt.Errorf("scan output dir %s: %w", out.Dir, err)
	}
	prevText := prevForLLM(rd, sel)
	facts := buildFacts(meta, sRed, status, git, rd)
	system, err := buildSystemPrompt(cfg)
	if err != nil {
		return err
	}

	res, err := a.summarize(ctx, u, flags, cfg, rd, sRed, status, system, facts, prevText)
	if err != nil {
		return err
	}
	doc, err := handoff.Build(handoff.BuildOptions{
		Harness:      meta.Harness,
		SessionID:    meta.ID,
		Title:        cmp.Or(sRed.Title, meta.ID),
		GeneratedAt:  time.Now(),
		Host:         hostName(),
		ToolVersion:  a.version,
		Model:        res.Model,
		Status:       status,
		Todos:        sRed.Todos,
		Repo:         repoState(git),
		FilesTouched: touchedFiles(rd, sRed, git),
		Summary:      res.Summary,
		Raw:          flags.Raw,
		Transcript:   res.Reduced,
		History:      sel.NewHistory(),
	})
	if err != nil {
		return err
	}

	disp, err := a.writeDoc(out, doc)
	if err != nil {
		return err
	}
	u.Final(disp, len(doc.Content), res.Before, res.After, int(rd.total.Load()))
	for _, w := range res.Warnings {
		u.Warn(w)
	}
	for _, w := range git.Warnings {
		u.Warn(w)
	}
	if sRed.Skipped > 0 {
		u.Warn(fmt.Sprintf("skipped %d unparseable lines", sRed.Skipped))
	}
	return nil
}

// hostName is the machine name for the meta comment.
func hostName() string {
	h, _ := os.Hostname()
	return h
}

// loadWithSpinner wraps session loading in a "Reading session…" step.
func (a *app) loadWithSpinner(ctx context.Context, u *ui.UI, ads []harness.Adapter, meta session.Meta) (session.Session, session.Info, error) {
	sp := u.Step("Reading session")
	defer sp.Stop()
	return loadSession(ctx, ads, meta)
}

// gitWithSpinner wraps gitinfo.Info in a "Collecting git state…" step.
func (a *app) gitWithSpinner(ctx context.Context, u *ui.UI, cwd string, rec session.GitRecord) (gitinfo.Result, error) {
	sp := u.Step("Collecting git state")
	defer sp.Stop()
	res, err := gitinfo.Info(ctx, cwd, rec)
	if err != nil {
		return gitinfo.Result{}, err
	}
	return res, nil
}

// redactWithSpinner applies redaction to everything that leaves the process
// before truncation: turns, title, cwd, todos.
func redactWithSpinner(u *ui.UI, rd *redactor, s session.Session) session.Session {
	sp := u.Step("Redacting")
	defer sp.Stop()
	return redactSession(rd, s)
}

// redactSession returns a copy of the session with every displayed string
// redacted (full values, before any truncation). Cwd stays on Meta for
// reference but is only used for display after this point.
func redactSession(rd *redactor, s session.Session) session.Session {
	out := s
	out.Title = rd.str(s.Title)
	out.Cwd = rd.str(s.Cwd)
	out.Turns = make([]session.Turn, len(s.Turns))
	for i, t := range s.Turns {
		t.Text = rd.str(t.Text)
		t.Input = rd.str(t.Input)
		out.Turns[i] = t
	}
	out.Todos = make([]session.Todo, len(s.Todos))
	for i, td := range s.Todos {
		td.Content = rd.str(td.Content)
		out.Todos[i] = td
	}
	return out
}

// prevForLLM extracts and redacts the previous handoff text for the prompt.
func prevForLLM(rd *redactor, sel handoff.Selection) string {
	if sel.Prev == nil {
		return ""
	}
	return rd.str(sel.ForLLM())
}

// repoState mirrors gitinfo.Result into handoff.RepoState with redacted
// git output.
func repoState(git gitinfo.Result) handoff.RepoState {
	return handoff.RepoState{
		Source:          git.Source,
		RepoURL:         git.RepoURL,
		Branch:          git.Branch,
		Head:            git.Head,
		HeadSubject:     git.HeadSubject,
		StatusLines:     git.StatusLines,
		DiffStatSummary: git.DiffStatSummary,
		DiffStatFiles:   git.DiffStatFiles,
		Ahead:           git.Ahead,
		Behind:          git.Behind,
		UpstreamMissing: git.UpstreamMissing,
		Warnings:        git.Warnings,
	}
}

// touchedFiles makes the session's files repository-relative and redacts
// them.
func touchedFiles(rd *redactor, s session.Session, git gitinfo.Result) []string {
	rel := gitinfo.Relativize(s.FilesTouched, git.Root)
	out := make([]string, len(rel))
	for i, f := range rel {
		out[i] = rd.str(f)
	}
	return out
}

// buildSystemPrompt assembles the system prompt: the built-in prompt or a
// replacement file, then --prompt-file text, then --prompt-append text.
func buildSystemPrompt(cfg *config.Config) (string, error) {
	base := ""
	if cfg.SystemPromptFile != "" {
		b, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return "", fmt.Errorf("%w: read system-prompt-file: %v", cli.ErrUsage, err)
		}
		base = string(b)
	} else {
		base = handoff.SystemPrompt(nil)
	}
	var extras []string
	if cfg.PromptFile != "" {
		b, err := os.ReadFile(cfg.PromptFile)
		if err != nil {
			return "", fmt.Errorf("%w: read prompt-file: %v", cli.ErrUsage, err)
		}
		extras = append(extras, string(b))
	}
	extras = append(extras, cfg.PromptAppend)
	return base + appendExtras(extras), nil
}

// appendExtras mirrors handoff.SystemPrompt's "Additional instructions"
// formatting, which its API cannot express over a replacement base prompt.
func appendExtras(extras []string) string {
	var out strings.Builder
	for _, e := range extras {
		if e == "" {
			continue
		}
		out.WriteString("\n\nAdditional instructions from the user:\n" + e)
	}
	return out.String()
}

// summaryResult carries the summarize phase's outputs.
type summaryResult struct {
	Reduced  string
	Summary  string
	Before   int
	After    int
	Model    string
	Warnings []string
}

// summarize reduces the transcript and, unless --raw, runs the LLM with one
// validation re-roll.
func (a *app) summarize(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config, rd *redactor,
	s session.Session, status session.Status, system, facts, prevText string) (summaryResult, error) {

	before := estimateTokens(s)
	if flags.Raw {
		return a.rawReduce(ctx, u, cfg, s, system, facts, prevText, before)
	}
	client, err := a.buildClient(cfg, rd)
	if err != nil {
		return summaryResult{}, err
	}
	sp := u.Step("Summarizing with " + cfg.Model)
	defer sp.Stop()

	var chunks atomic.Int64
	reducer := reduce.Reducer{
		MaxInputTokens:  cfg.MaxInputTokens,
		SystemPromptLen: len(system),
		FactsLen:        len(facts),
		PrevLen:         len(prevText),
		Summarizer: func(ctx context.Context, chunk string) (string, error) {
			sp.Update(fmt.Sprintf("summarizing chunk %d", chunks.Add(1)))
			return client.Summarize(ctx, handoff.ChunkPrompt(), chunk)
		},
	}
	reduced, err := reducer.Reduce(ctx, s)
	if err != nil {
		return summaryResult{}, llmPhase(err)
	}
	summary, warnings, err := a.completeAndValidate(ctx, client, rd, sp, status, system, facts, prevText, reduced)
	if err != nil {
		return summaryResult{}, err
	}
	return summaryResult{
		Reduced:  reduced,
		Summary:  summary,
		Before:   before,
		After:    reduce.EstimateTokens(reduced),
		Model:    cfg.Model,
		Warnings: warnings,
	}, nil
}

// rawReduce runs reduce in raw mode: no LLM, the reduced transcript becomes
// the file's Summary section replacement.
func (a *app) rawReduce(ctx context.Context, u *ui.UI, cfg *config.Config, s session.Session,
	system, facts, prevText string, before int) (summaryResult, error) {
	sp := u.Step("Reducing transcript")
	defer sp.Stop()
	red := reduce.Reducer{
		MaxInputTokens:  cfg.MaxInputTokens,
		SystemPromptLen: len(system),
		FactsLen:        len(facts),
		PrevLen:         len(prevText),
		Raw:             true,
	}
	text, err := red.Reduce(ctx, s)
	if err != nil {
		return summaryResult{}, llmPhase(err)
	}
	return summaryResult{Reduced: text, Before: before, After: reduce.EstimateTokens(text)}, nil
}

// completeAndValidate asks the LLM for the Summary and validates it with at
// most one re-roll.
func (a *app) completeAndValidate(ctx context.Context, client *llm.Client, rd *redactor, sp *spinner,
	status session.Status, system, facts, prevText, reduced string) (string, []string, error) {

	msg := handoff.UserMessage(handoff.UserMessageInput{
		PreviousHandoff: prevText,
		SessionFacts:    facts,
		Transcript:      reduced,
	})
	answer, _, err := client.Complete(ctx, system, msg)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", errLLM, err)
	}
	reRolled := false
	opts := handoff.ValidateOptions{
		Redact:      rd.fn(),
		Status:      status.Text,
		PendingTool: status.PendingToolName,
		Reroll: func(missing []string) (string, error) {
			reRolled = true
			sp.Update("re-roll: filling missing sections")
			return client.Summarize(ctx, system, handoff.RerollMessage(missing))
		},
	}
	vres := handoff.Validate(answer, opts)
	warnings := append([]string{}, vres.Warnings...)
	if reRolled {
		warnings = append(warnings, "summary was re-rolled once")
	}
	return rd.str(vres.Clean), warnings, nil
}

// llmPhase classifies reduce-phase failures: cancellation stays itself so it
// maps to 130, everything else (including "transcript too large") maps to 4.
func llmPhase(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %v", errLLM, err)
}

// buildClient constructs the LLM client from the merged config.
func (a *app) buildClient(cfg *config.Config, rd *redactor) (*llm.Client, error) {
	return llm.New(llm.Options{
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		APIKey:          cfg.APIKey(a.getenv),
		Version:         a.version,
		MaxOutputTokens: cfg.MaxOutputTokens,
		ThinkingStyle:   cfg.ThinkingStyle,
		ThinkingLevel:   cfg.Thinking,
		ExtraBody:       json.RawMessage(cfg.ExtraBody),
		Redact:          rd.fn(),
		HTTPClient:      &http.Client{Timeout: cfg.Timeout},
	})
}

// estimateTokens estimates the unreduced transcript size in tokens.
func estimateTokens(s session.Session) int {
	n := 0
	for _, t := range s.Turns {
		n += len(t.Text) + len(t.Input)
	}
	return n / 4
}

// writeDoc writes the document to the output path or stdout and returns the
// display name for the final line.
func (a *app) writeDoc(out output, doc handoff.Document) (string, error) {
	if out.Stdout {
		if _, err := fmt.Fprint(a.stdout, doc.Content); err != nil {
			return "", fmt.Errorf("write stdout: %w", err)
		}
		return "-", nil
	}
	if err := handoff.WriteFile(out.Path, []byte(doc.Content)); err != nil {
		return "", fmt.Errorf("write %s: %w", out.Path, err)
	}
	return out.Disp, nil
}

// spinner is the concrete type of a progress step, used by the re-roll
// callback to update the text.
type spinner = ui.Spinner
