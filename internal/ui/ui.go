// Package ui renders the command-line experience to stderr: TTY detection,
// colors, the session picker, the spinner, the overwrite confirm and the
// final status line. Everything goes to stderr, so `-o -` keeps stdout clean.
package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrCancelled reports that the user cancelled a prompt (Esc, q, Ctrl-C or
// EOF); the CLI maps it to exit code 130.
var ErrCancelled = errors.New("cancelled by user")

// ErrNoSessions reports an empty session list; the CLI maps it to exit 3.
var ErrNoSessions = errors.New("no sessions")

// Options configures New.
type Options struct {
	Quiet   bool
	NoColor bool
	// Env reads the process environment; injectable for tests.
	Env func(string) string
}

// UI is the shared output state.
type UI struct {
	Stderr io.Writer
	Stdin  *os.File

	InTTY  bool
	ErrTTY bool
	Color  bool
	Quiet  bool

	width int
}

// New builds a UI. inTTY/errTTY report whether stdin/stderr are terminals.
func New(stderr io.Writer, stdin *os.File, inTTY, errTTY bool, opts Options) *UI {
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	u := &UI{
		Stderr: stderr,
		Stdin:  stdin,
		InTTY:  inTTY,
		ErrTTY: errTTY,
		Color:  errTTY && !opts.NoColor && env("NO_COLOR") == "",
		Quiet:  opts.Quiet,
	}
	u.width = defaultWidth
	if u.InTTY {
		if w := terminalWidth(stdin); w > 0 {
			u.width = w
		}
	}
	return u
}

// DetectTTY reports whether f is a terminal: a character device that is not
// /dev/null (whose stat also reports a character device). It is the stdlib
// stand-in for x/term.IsTerminal, which is not a dependency.
func DetectTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if devNull, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, devNull) {
		return false
	}
	return true
}

const defaultWidth = 100

// terminalWidth shells out to stty on the TTY; 0 means unknown.
func terminalWidth(stdin *os.File) int {
	if runtime.GOOS == "windows" {
		return 0
	}
	cmd := exec.Command("stty", "size")
	cmd.Stdin = stdin
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0
	}
	var cols int
	if _, err := fmt.Sscanf(fields[1], "%d", &cols); err != nil {
		return 0
	}
	return cols
}

func (u *UI) colorize(code, s string) string {
	if !u.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) bold(s string) string   { return u.colorize("1", s) }
func (u *UI) green(s string) string  { return u.colorize("32", s) }
func (u *UI) yellow(s string) string { return u.colorize("33", s) }
func (u *UI) dim(s string) string    { return u.colorize("2", s) }
func (u *UI) printLine(s string) {
	_, _ = fmt.Fprintln(u.Stderr, s)
}
