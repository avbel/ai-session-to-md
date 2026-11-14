package codex

import (
	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// Adapter reads Codex local storage: the newest state_*.sqlite threads
// database plus the rollout JSONL files it references, with a filesystem
// fallback for stores that predate the database.
type Adapter struct {
	dir  string
	log  func(format string, args ...any)
	info infoBySession
}

// Options carries the wiring knobs for New.
type Options struct {
	// Log receives --verbose-style diagnostics: dropped rows, unknown
	// injected tags, fallback switches. Nil-safe: a nil Log discards output.
	Log func(format string, args ...any)
}

type Option func(*Options)

// WithLogger wires the --verbose log hook; nil restores the silent default.
func WithLogger(f func(format string, args ...any)) Option {
	return func(o *Options) {
		if f != nil {
			o.Log = f
		}
	}
}

// New builds an Adapter rooted at the Codex storage dir ($CODEX_HOME).
func New(dir string, opts ...Option) *Adapter {
	o := Options{}
	for _, opt := range opts {
		opt(&o)
	}
	log := o.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Adapter{dir: dir, log: log}
}

func (a *Adapter) Name() session.Harness {
	return session.HarnessCodex
}

// infoBySession keeps the end-of-session signals Load derived for each
// session. session.Session cannot carry them (its shape is frozen after
// wave 1), so callers read them back by session id.
type infoBySession map[string]session.Info

// rememberInfo records the Info for a loaded session id.
func (a *Adapter) rememberInfo(id string, info session.Info) {
	if a.info == nil {
		a.info = infoBySession{}
	}
	a.info[id] = info
}

// StatusInfo returns the harness-specific Info for a loaded session: Codex
// records MidTurn from task_started/task_complete events. Unknown ids get
// the zero Info.
func (a *Adapter) StatusInfo(id string) session.Info {
	return a.info[id]
}

// DeriveStatus is the Codex-aware wrapper around session.Derive: callers
// without access to the raw event stream should prefer it over calling
// session.Derive with a zero Info, which cannot see "stopped mid-turn".
func (a *Adapter) DeriveStatus(s session.Session) session.Status {
	return session.Derive(s, a.StatusInfo(s.ID))
}

var _ harness.Adapter = (*Adapter)(nil)
