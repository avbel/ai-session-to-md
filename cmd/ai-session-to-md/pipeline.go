package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
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

// homeKind is the redactor's kind name for home-path folding; folded paths
// are not secrets, so they are excluded from the reported counts.
const homeKind = "home"

// redactor wraps the secret redactor and collects every redaction, by kind,
// for the final status line.
type redactor struct {
	r     *redact.Redactor
	mu    sync.Mutex
	kinds map[string]int
}

func newRedactor(patterns []string) (*redactor, error) {
	r, err := redact.New(redact.WithPatterns(patterns))
	if err != nil {
		return nil, fmt.Errorf("build redactor: %w", err)
	}
	return &redactor{r: r, kinds: map[string]int{}}, nil
}

// str redacts one string and collects the matches.
func (rd *redactor) str(s string) string {
	res := rd.r.Apply(s)
	rd.mu.Lock()
	for kind, n := range res.Counts {
		if kind == homeKind {
			continue
		}
		rd.kinds[kind] += n
	}
	rd.mu.Unlock()
	return res.Text
}

// fn is the plain func form, used for validate's secret check and the LLM
// client's error redaction.
func (rd *redactor) fn() func(string) string {
	return rd.str
}

// summary snapshots the collected counts for the final status line, kinds
// ordered most frequent first.
func (rd *redactor) summary() ui.Redactions {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	type kindCount struct {
		kind string
		n    int
	}
	var cs []kindCount
	out := ui.Redactions{}
	for kind, n := range rd.kinds {
		out.Total += n
		cs = append(cs, kindCount{kind, n})
	}
	slices.SortFunc(cs, func(a, b kindCount) int {
		if a.n != b.n {
			return b.n - a.n
		}
		return strings.Compare(a.kind, b.kind)
	})
	for _, c := range cs {
		out.Kinds = append(out.Kinds, c.kind)
	}
	return out
}

// runHandoff drives load → git → redact → reduce → summarize → write.
func (a *app) runHandoff(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config,
	log func(string), ads []harness.Adapter, meta session.Meta) error {

	rd, err := newRedactor(cfg.RedactPatterns)
	if err != nil {
		return err
	}
	out, err := a.outputPlan(ctx, u, flags, meta)
	if err != nil {
		return err
	}

	s, info, err := a.loadWithSpinner(ctx, u, ads, meta)
	if err != nil {
		return err
	}
	// gitinfo measures the session's own cwd (the last non-empty one on the
	// transcript), never the redacted picker display copy.
	git, err := a.gitWithSpinner(ctx, u, s.Cwd, recordedGit(s))
	if err != nil {
		return err
	}
	// Redact the git output once: the same values feed the facts JSON, the
	// handoff's repository section and the warnings.
	git = redactGit(rd, git)
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

	res, err := a.summarize(ctx, u, flags, cfg, rd, log, sRed, status, system, facts, prevText)
	if err != nil {
		return err
	}
	if res.ReRolled {
		log("summary was re-rolled once")
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

	disp, err := a.writeDoc(u, out, doc)
	if err != nil {
		return err
	}
	u.Final(disp, len(doc.Content), res.Before, res.After, rd.summary())
	// Warnings: git state first, then skipped lines, then summary quality.
	for _, w := range git.Warnings {
		u.Warn(w)
	}
	if sRed.Skipped > 0 {
		u.Warn(fmt.Sprintf("skipped %d unparseable lines", sRed.Skipped))
	}
	for _, w := range res.Warnings {
		u.Warn(w)
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
	return gitinfo.Info(ctx, cwd, rec)
}

// recordedGit is the RecordedGit record gitinfo should fall back to. Branch
// doubles as the session's recorded branch for the mismatch warning, taking
// Meta.Branch when the record itself carries none (Claude Code).
func recordedGit(s session.Session) session.GitRecord {
	rec := s.RecordedGit
	rec.Branch = cmp.Or(rec.Branch, s.Branch)
	return rec
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

// redactGit returns a copy of the git result with every output string
// redacted. Branch, head and warnings all reach the handoff file.
func redactGit(rd *redactor, git gitinfo.Result) gitinfo.Result {
	out := git
	out.RepoURL = rd.str(git.RepoURL)
	out.Branch = rd.str(git.Branch)
	out.Head = rd.str(git.Head)
	out.HeadSubject = rd.str(git.HeadSubject)
	out.DiffStatSummary = rd.str(git.DiffStatSummary)
	out.StatusLines = redactAll(rd, git.StatusLines)
	out.DiffStatFiles = redactAll(rd, git.DiffStatFiles)
	out.Warnings = redactAll(rd, git.Warnings)
	return out
}

func redactAll(rd *redactor, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = rd.str(l)
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

// repoState mirrors the already-redacted git result into handoff.RepoState.
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
		b, err := os.ReadFile(cfg.SystemPromptFile) //nolint:gosec // G304: the path is the user's own config value
		if err != nil {
			return "", fmt.Errorf("%w: read system-prompt-file: %v", cli.ErrUsage, err)
		}
		base = string(b)
	} else {
		base = handoff.SystemPrompt(nil)
	}
	var extras []string
	if cfg.PromptFile != "" {
		b, err := os.ReadFile(cfg.PromptFile) //nolint:gosec // G304: the path is the user's own config value
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
	ReRolled bool
	Warnings []string
}

// summarize reduces the transcript and, unless --raw, runs the LLM with one
// validation re-roll.
func (a *app) summarize(ctx context.Context, u *ui.UI, flags *cli.Flags, cfg *config.Config, rd *redactor,
	log func(string), s session.Session, status session.Status, system, facts, prevText string) (summaryResult, error) {

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

	log(fmt.Sprintf("input parts, estimated tokens: transcript %d, session facts %d, previous handoff %d, system prompt %d",
		before, reduce.EstimateTokens(facts), reduce.EstimateTokens(prevText), reduce.EstimateTokens(system)))

	reducer := reduce.Reducer{
		MaxInputTokens:  cfg.MaxInputTokens,
		SystemPromptLen: len(system),
		FactsLen:        len(facts),
		PrevLen:         len(prevText),
		Summarizer: func(ctx context.Context, chunk string) (string, error) {
			return client.Summarize(ctx, handoff.ChunkPrompt(), chunk)
		},
		OnChunk: func(done, total int, phase string) {
			switch phase {
			case "older":
				sp.Update(fmt.Sprintf("Summarizing with %s · older history %d/%d", cfg.Model, done, total))
			case "notes":
				sp.Update(fmt.Sprintf("Summarizing with %s · condensing notes %d/%d", cfg.Model, done, total))
			}
		},
	}
	reduced, err := reducer.Reduce(ctx, s)
	if err != nil {
		return summaryResult{}, llmPhase(err)
	}
	sp.Update("Summarizing with " + cfg.Model + " · writing summary")
	summary, warnings, reRolled, err := a.completeAndValidate(ctx, client, rd, sp, log, status, system, facts, prevText, reduced)
	if err != nil {
		return summaryResult{}, err
	}
	return summaryResult{
		Reduced:  reduced,
		Summary:  summary,
		Before:   before,
		After:    reduce.EstimateTokens(reduced),
		Model:    cfg.Model,
		ReRolled: reRolled,
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
// most one re-roll. The re-roll continues the same conversation: the model
// sees the system prompt, its own first answer and the original user message
// again next to the re-roll request.
func (a *app) completeAndValidate(ctx context.Context, client *llm.Client, rd *redactor, sp *spinner,
	log func(string), status session.Status, system, facts, prevText, reduced string) (string, []string, bool, error) {

	msg := handoff.UserMessage(handoff.UserMessageInput{
		PreviousHandoff: prevText,
		SessionFacts:    facts,
		Transcript:      reduced,
	})
	answer, _, err := client.Complete(ctx, system, msg)
	if err != nil {
		return "", nil, false, fmt.Errorf("%w: %w", errLLM, err)
	}
	reRolled := false
	opts := handoff.ValidateOptions{
		Redact:      rd.fn(),
		Status:      status.Text,
		PendingTool: status.PendingToolName,
		Reroll: func(missing []string) (string, error) {
			reRolled = true
			sp.Update("summary incomplete, asking again")
			log("re-roll: missing " + strings.Join(missing, ", "))
			text, _, err := client.Chat(ctx, []llm.Message{
				{Role: "system", Content: system},
				{Role: "user", Content: msg},
				{Role: "assistant", Content: answer},
				{Role: "user", Content: handoff.RerollMessage(missing)},
			})
			return text, err
		},
	}
	vres := handoff.Validate(answer, opts)
	if vres.Err != nil {
		// A cancelled re-roll exits as 130 (the wrapped context.Canceled wins
		// in exitFor); a per-request timeout stays an LLM failure (exit 4).
		if errors.Is(vres.Err, context.DeadlineExceeded) {
			return "", nil, reRolled, fmt.Errorf("%w: %w", errLLM, vres.Err)
		}
		return "", nil, reRolled, vres.Err
	}
	return rd.str(vres.Clean), vres.Warnings, reRolled, nil
}

// llmPhase classifies reduce-phase failures: cancellation stays itself so it
// maps to 130, everything else (including "transcript too large") maps to 4.
func llmPhase(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %w", errLLM, err)
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
func (a *app) writeDoc(u *ui.UI, out output, doc handoff.Document) (string, error) {
	if out.Stdout {
		if _, err := fmt.Fprint(a.stdout, doc.Content); err != nil {
			return "", fmt.Errorf("write stdout: %w", err)
		}
		return "-", nil
	}
	sp := u.Step("Writing " + out.Disp)
	defer sp.Stop()
	if err := handoff.WriteFile(out.Path, []byte(doc.Content)); err != nil {
		return "", fmt.Errorf("write %s: %w", out.Path, err)
	}
	return out.Disp, nil
}

// spinner is the concrete type of a progress step, used by the re-roll
// callback to update the text.
type spinner = ui.Spinner
