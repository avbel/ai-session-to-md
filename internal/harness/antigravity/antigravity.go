// Package antigravity reads Google Antigravity's local store (agy CLI and the
// Antigravity app), rooted at ~/.gemini.
//
// Each app keeps a readable step log per conversation at
// <root>/<app>/brain/<id>/.system_generated/logs/transcript_full.jsonl (the
// untruncated log; transcript.jsonl is the truncated sibling and the only one
// the app writes). The per-conversation conversations/<id>.db SQLite files and
// the app's .pb files hold undocumented or encrypted protobuf and are not
// read. Listing metadata (workspaces, titles, run state) comes from the agy
// index antigravity-cli/conversation_summaries.db, which also indexes the
// app's conversations; without it the adapter falls back to history.jsonl and
// the transcripts themselves.
package antigravity

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

const (
	cliApp        = "antigravity-cli"
	desktopApp    = "antigravity"
	summariesFile = "conversation_summaries.db"
	historyFile   = "history.jsonl"
	fullLog       = "transcript_full.jsonl"
	truncatedLog  = "transcript.jsonl"
	taskFile      = "task.md"
)

// apps are the app data dirs under the root, in lookup order: a conversation
// present in both is read from the CLI, whose log is untruncated.
var apps = []string{cliApp, desktopApp}

// idPattern is the conversation id shape agy uses (a UUID). It guards every
// path built from an id or from the summaries index.
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Adapter implements harness.Adapter for Antigravity.
type Adapter struct {
	root string
	log  func(format string, args ...any)
	warn func(string)
}

// Option configures New.
type Option func(*Adapter)

// WithLogger routes per-item diagnostics (skipped conversations, an
// unreadable index) to fn, the CLI's --verbose sink.
func WithLogger(fn func(format string, args ...any)) Option {
	return func(a *Adapter) {
		if fn != nil {
			a.log = fn
		}
	}
}

// WithWarn routes warnings the user should see to fn: an index that exists
// but cannot be read, which also disables subagent filtering.
func WithWarn(fn func(string)) Option {
	return func(a *Adapter) {
		if fn != nil {
			a.warn = fn
		}
	}
}

// New builds the adapter for root, normally ~/.gemini.
func New(root string, opts ...Option) *Adapter {
	a := &Adapter{root: root, log: func(string, ...any) {}, warn: func(string) {}}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Adapter) Name() session.Harness {
	return session.HarnessAntigravity
}

// Load reads one conversation's transcript.
func (a *Adapter) Load(ctx context.Context, m session.Meta) (session.Session, error) {
	s, _, err := a.LoadDetailed(ctx, m)
	return s, err
}

// LoadDetailed is Load plus the end-of-session signals Derive needs: the
// mid-turn state from the index and a trailing model error.
func (a *Adapter) LoadDetailed(ctx context.Context, m session.Meta) (session.Session, session.Info, error) {
	if !idPattern.MatchString(m.ID) {
		return session.Session{}, session.Info{}, fmt.Errorf("antigravity: invalid conversation id %q", m.ID)
	}
	path, app, err := a.locate(m)
	if err != nil {
		return session.Session{}, session.Info{}, err
	}
	recs, skipped, err := readRecords(ctx, path)
	if err != nil {
		return session.Session{}, session.Info{}, err
	}
	b := newBuilder(filepath.Join(a.root, app, "brain", m.ID))
	b.addAll(recs)

	sum, hasSum := a.summaryFor(ctx, m.ID)
	s := session.Session{
		Meta:         m,
		Model:        b.model,
		Turns:        b.turns,
		Todos:        readTasks(filepath.Join(a.root, app, "brain", m.ID, taskFile)),
		FilesTouched: b.files,
		Skipped:      skipped,
	}
	s.Harness = session.HarnessAntigravity
	s.Source = path
	if s.Cwd == "" {
		s.Cwd = a.fallbackCwd(m.ID, recs)
	}
	if s.Title == "" {
		s.Title = cmp.Or(a.annotationTitle(app, m.ID), sum.title, firstRequest(recs))
	}
	if st, err := os.Stat(path); err == nil && st.ModTime().After(s.UpdatedAt) {
		s.UpdatedAt = st.ModTime()
	}
	info := session.Info{MidTurn: hasSum && sum.busy()}
	if b.endedOnError {
		info.HasFinish, info.Finish = true, "error"
	}
	return s, info, nil
}

// locate finds the transcript: the listed Source when it is one of this
// conversation's logs, else the first app dir holding a log for the id.
func (a *Adapter) locate(m session.Meta) (path, app string, err error) {
	for _, app := range apps {
		logs := logsDir(a.root, app, m.ID)
		if m.Source == filepath.Join(logs, fullLog) || m.Source == filepath.Join(logs, truncatedLog) {
			if isFile(m.Source) {
				return m.Source, app, nil
			}
		}
	}
	for _, app := range apps {
		if p := transcriptIn(logsDir(a.root, app, m.ID)); p != "" {
			return p, app, nil
		}
	}
	return "", "", fmt.Errorf("antigravity: no transcript for conversation %s under %s", m.ID, a.root)
}

func logsDir(root, app, id string) string {
	return filepath.Join(root, app, "brain", id, ".system_generated", "logs")
}

// transcriptIn prefers the untruncated log; "" when neither exists.
func transcriptIn(logs string) string {
	for _, name := range []string{fullLog, truncatedLog} {
		if p := filepath.Join(logs, name); isFile(p) {
			return p
		}
	}
	return ""
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

var _ harness.Adapter = (*Adapter)(nil)
