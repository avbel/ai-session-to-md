// Package cli parses the ai-session-to-md command line with the stdlib flag
// package. Flags may appear before and after the HARNESS and SESSION_ID
// positionals; config/env fallbacks are applied in internal/config.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// ErrUsage marks a command-line problem; the CLI maps it to exit code 2.
var ErrUsage = errors.New("usage error")

// Defaults for the flags whose values also live in the config file.
const (
	DefaultLimit          = 50
	DefaultMaxInputTokens = 100000
	DefaultTimeout        = 10 * time.Minute
)

// Flags is the parsed command line.
type Flags struct {
	Harness   session.Harness
	SessionID string

	All      bool
	Archived bool
	Latest   bool
	Limit    int

	Output string
	Force  bool
	Raw    bool

	BaseURL         string
	Model           string
	APIKeyEnv       string
	Thinking        string
	ThinkingStyle   string
	ExtraBody       string
	MaxInputTokens  int
	MaxOutputTokens int
	Timeout         time.Duration

	PromptAppend     string
	PromptFile       string
	SystemPromptFile string

	ClaudeDir   string
	CodexDir    string
	OpenCodeDir string

	ConfigPath string

	Quiet       bool
	Verbose     bool
	NoColor     bool
	ShowVersion bool
	ShowHelp    bool

	set map[string]bool
}

// aliases maps accepted positional spellings to canonical harness names.
var aliases = map[string]session.Harness{
	"claude-code": session.HarnessClaudeCode,
	"claude":      session.HarnessClaudeCode,
	"cc":          session.HarnessClaudeCode,
	"codex":       session.HarnessCodex,
	"cx":          session.HarnessCodex,
	"opencode":    session.HarnessOpenCode,
	"oc":          session.HarnessOpenCode,
}

// Parse loops fs.Parse over the argument list so that flags are accepted
// before, between and after the positionals. The first positional must be a
// harness name or alias, the second (optional) a session id prefix. The
// FlagSet is built once and reused: re-defining flags each round would
// reset their values to the defaults.
func Parse(args []string) (*Flags, error) {
	f := &Flags{
		Limit:          DefaultLimit,
		MaxInputTokens: DefaultMaxInputTokens,
		Timeout:        DefaultTimeout,
		set:            map[string]bool{},
	}
	fs := newFlagSet(f)
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				f.ShowHelp = true
				return f, nil
			}
			return nil, fmt.Errorf("%w: %v", ErrUsage, err)
		}
		fs.Visit(func(fl *flag.Flag) { f.set[canonical(fl.Name)] = true })
		if fs.NArg() == 0 {
			return f, nil
		}
		if err := f.takePositional(fs.Arg(0)); err != nil {
			return nil, err
		}
		rest = fs.Args()[1:]
		if len(rest) == 0 {
			return f, nil
		}
	}
}

// canonical maps the short output flag onto the long name.
func canonical(name string) string {
	if name == "o" {
		return "output"
	}
	return name
}

func (f *Flags) takePositional(pos string) error {
	if f.SessionID != "" {
		return fmt.Errorf("%w: unexpected argument %q", ErrUsage, pos)
	}
	if h, ok := aliases[pos]; ok {
		if f.Harness != "" && f.Harness != h {
			return fmt.Errorf("%w: harness given twice: %s and %s", ErrUsage, f.Harness, h)
		}
		f.Harness = h
		return nil
	}
	if err := harness.ValidatePrefix(pos); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	f.SessionID = pos
	return nil
}

// Value returns the string form of a flag that was given on the command
// line; ok is false for unset flags and names that carry no config meaning.
func (f *Flags) Value(name string) (string, bool) {
	if !f.set[name] {
		return "", false
	}
	switch name {
	case "output":
		return f.Output, true
	case "base-url":
		return f.BaseURL, true
	case "model":
		return f.Model, true
	case "api-key-env":
		return f.APIKeyEnv, true
	case "thinking":
		return f.Thinking, true
	case "thinking-style":
		return f.ThinkingStyle, true
	case "extra-body":
		return f.ExtraBody, true
	case "max-input-tokens":
		return strconv.Itoa(f.MaxInputTokens), true
	case "max-output-tokens":
		return strconv.Itoa(f.MaxOutputTokens), true
	case "timeout":
		return f.Timeout.String(), true
	case "prompt-append":
		return f.PromptAppend, true
	case "prompt-file":
		return f.PromptFile, true
	case "system-prompt-file":
		return f.SystemPromptFile, true
	case "claude-dir":
		return f.ClaudeDir, true
	case "codex-dir":
		return f.CodexDir, true
	case "opencode-dir":
		return f.OpenCodeDir, true
	case "config":
		return f.ConfigPath, true
	default:
		return "", false
	}
}

// Values returns every set flag that has a config meaning, in string form.
func (f *Flags) Values() map[string]string {
	out := map[string]string{}
	for _, name := range configFlagNames {
		if v, ok := f.Value(name); ok {
			out[name] = v
		}
	}
	return out
}

var configFlagNames = []string{
	"base-url", "model", "api-key-env", "thinking", "thinking-style",
	"extra-body", "max-input-tokens", "max-output-tokens", "timeout",
	"prompt-append", "prompt-file", "system-prompt-file",
	"claude-dir", "codex-dir", "opencode-dir", "config",
}

func newFlagSet(f *Flags) *flag.FlagSet {
	fs := flag.NewFlagSet("ai-session-to-md", flag.ContinueOnError)
	fs.SetOutput(errorSink{})

	boolFlag := func(p *bool, name, help string) {
		fs.BoolVar(p, name, false, help)
	}
	boolFlag(&f.All, "all", "list sessions from every project, not just this one")
	boolFlag(&f.Archived, "archived", "include archived sessions")
	boolFlag(&f.Latest, "latest", "take the newest session without asking")
	fs.IntVar(&f.Limit, "limit", DefaultLimit, "max sessions to list (default 50)")
	fs.StringVar(&f.Output, "output", "", "output path (`-` = stdout; default ./<NAME>.md)")
	fs.StringVar(&f.Output, "o", "", "shorthand for --output")
	boolFlag(&f.Force, "force", "overwrite the output file without asking")
	boolFlag(&f.Raw, "raw", "skip the LLM and write the reduced transcript")
	fs.StringVar(&f.BaseURL, "base-url", "", "OpenAI-compatible API base URL")
	fs.StringVar(&f.Model, "model", "", "model name (required unless --raw)")
	fs.StringVar(&f.APIKeyEnv, "api-key-env", "", "env var holding the API key")
	fs.StringVar(&f.Thinking, "thinking", "", "thinking level: off|minimal|low|medium|high|xhigh")
	fs.StringVar(&f.ThinkingStyle, "thinking-style", "", "thinking style: auto|openai|openrouter|none")
	fs.StringVar(&f.ExtraBody, "extra-body", "", "extra JSON object merged into the request body")
	fs.IntVar(&f.MaxInputTokens, "max-input-tokens", DefaultMaxInputTokens, "transcript input budget in tokens")
	fs.IntVar(&f.MaxOutputTokens, "max-output-tokens", 0, "max completion tokens")
	fs.DurationVar(&f.Timeout, "timeout", DefaultTimeout, "HTTP timeout per request (default 10m)")
	fs.StringVar(&f.PromptAppend, "prompt-append", "", "extra instructions appended to the system prompt")
	fs.StringVar(&f.PromptFile, "prompt-file", "", "file whose text is appended to the system prompt")
	fs.StringVar(&f.SystemPromptFile, "system-prompt-file", "", "replace the built-in system prompt")
	fs.StringVar(&f.ClaudeDir, "claude-dir", "", "Claude Code storage dir")
	fs.StringVar(&f.CodexDir, "codex-dir", "", "Codex storage dir")
	fs.StringVar(&f.OpenCodeDir, "opencode-dir", "", "OpenCode storage dir")
	fs.StringVar(&f.ConfigPath, "config", "", "config file path")
	boolFlag(&f.Quiet, "quiet", "print only errors and the final path")
	boolFlag(&f.Verbose, "verbose", "log debug details to stderr")
	boolFlag(&f.NoColor, "no-color", "disable colored output")
	boolFlag(&f.ShowVersion, "version", "print the version and exit")
	boolFlag(&f.ShowHelp, "help", "print this help and exit")
	fs.BoolVar(&f.ShowHelp, "h", false, "shorthand for --help")
	return fs
}

// errorSink discards the flag package's own error output; Parse callers
// print a single formatted message instead.
type errorSink struct{}

func (errorSink) Write(p []byte) (int, error) { return len(p), nil }
