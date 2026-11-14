package cli

import (
	"fmt"
	"io"
	"strings"
)

const usageText = `ai-session-to-md [HARNESS] [SESSION_ID] [flags]

Turn an AI coding-agent session (Claude Code, Codex or OpenCode) into a
Markdown handoff file for the next agent, on any machine.

HARNESS      claude-code | codex | opencode (aliases: claude, cc / cx / oc).
             Omitted: all harnesses.
SESSION_ID   full id or unique prefix (>= 6 chars). Omitted: picker.

Selection:
  --all                 list sessions from every project
  --archived            include archived sessions
  --latest              take the newest session without asking
  --limit N             max sessions to list (default 50)

Output:
  -o, --output PATH     output path ("-" = stdout; default ./<NAME>.md)
  --force               overwrite the output file without asking
  --raw                 skip the LLM, write the reduced transcript

LLM:
  --base-url URL        OpenAI-compatible API base URL
  --model NAME          model name (required unless --raw)
  --api-key-env NAME    env var holding the API key
  --thinking LEVEL      off|minimal|low|medium|high|xhigh
  --thinking-style S    auto|openai|openrouter|none
  --extra-body JSON     extra JSON object merged into the request body
  --max-input-tokens N  transcript input budget (default 100000)
  --max-output-tokens N max completion tokens
  --timeout DUR         HTTP timeout per request (default 10m)

Prompt:
  --prompt-append TEXT  extra instructions appended to the system prompt
  --prompt-file PATH    file appended to the system prompt (before TEXT)
  --system-prompt-file PATH
                        replace the built-in system prompt

Storage:
  --claude-dir DIR      Claude Code storage dir (default ~/.claude)
  --codex-dir DIR       Codex storage dir (default ~/.codex)
  --opencode-dir DIR    OpenCode storage dir (default ~/.local/share/opencode)

Misc:
  --config PATH         config file (default $XDG_CONFIG_HOME or
                        ~/.config/ai-session-to-md/config.toml)
  --quiet               print only errors and the final path
  --verbose             log debug details to stderr
  --no-color            disable colored output
  --version             print the version and exit
  -h, --help            print this help and exit

Exit codes: 0 ok · 1 runtime error · 2 usage/config error ·
3 no sessions found · 4 LLM error · 130 cancelled.`

// PrintUsage writes the full help text.
func PrintUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, strings.TrimRight(usageText, "\n"))
}

// PickDecision is the pure selection rule: a session id always wins, then
// --latest, then the picker on a TTY. Without a TTY and neither id nor
// --latest it is a usage error.
func PickDecision(hasID, latest, tty bool) error {
	switch {
	case hasID, latest:
		return nil
	case tty:
		return nil
	default:
		return fmt.Errorf("%w: no TTY: pass a session id or --latest", ErrUsage)
	}
}

// OverwriteState is what the wiring knows about the output path.
type OverwriteState int

const (
	OverwriteAbsent OverwriteState = iota
	OverwriteSymlink
	OverwriteExists
)

// OverwriteDecision is the pure overwrite rule: a symlink is always refused,
// an existing file needs --force or a TTY confirm, a fresh path is fine.
func OverwriteDecision(state OverwriteState, force, tty bool) error {
	switch state {
	case OverwriteAbsent:
		return nil
	case OverwriteSymlink:
		return fmt.Errorf("%w: output path is a symlink", ErrUsage)
	default:
		if force {
			return nil
		}
		if !tty {
			return fmt.Errorf("%w: output file exists; pass --force to overwrite (no TTY to confirm)", ErrUsage)
		}
		return nil
	}
}
