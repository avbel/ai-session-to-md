// Package ui renders the command-line experience to stderr: TTY detection,
// colors, the session picker, the spinner, the overwrite confirm and the
// final status line. Everything goes to stderr, so `-o -` keeps stdout clean.
package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// ErrCancelled reports that the user cancelled a prompt (Esc, Ctrl-C or
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

	// spins counts spinners currently animating on the TTY; output printed
	// while one is active clears the spinner line first, so it does not glue
	// onto a spinner frame.
	spins atomic.Int64
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

// DetectTTY reports whether f is a terminal. A nil file (test injection) is
// never a terminal.
func DetectTTY(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}

const defaultWidth = 100

// terminalWidth asks the terminal driver for the column count; 0 means
// unknown.
func terminalWidth(f *os.File) int {
	if f == nil {
		return 0
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return w
}

var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDim    = lipgloss.NewStyle().Faint(true)
)

func (u *UI) bold(s string) string   { return u.paint(styleBold, s) }
func (u *UI) green(s string) string  { return u.paint(styleGreen, s) }
func (u *UI) yellow(s string) string { return u.paint(styleYellow, s) }
func (u *UI) red(s string) string    { return u.paint(styleRed, s) }
func (u *UI) dim(s string) string    { return u.paint(styleDim, s) }

// paint renders s with the style only when color is on, so the plain output
// is byte-identical to the unstyled string.
func (u *UI) paint(style lipgloss.Style, s string) string {
	if !u.Color {
		return s
	}
	return style.Render(s)
}

func (u *UI) printLine(s string) {
	if u.spins.Load() > 0 {
		// A spinner is animating: clear its line first so this line does not
		// glue onto a frame.
		_, _ = fmt.Fprint(u.Stderr, "\r\x1b[2K")
	}
	_, _ = fmt.Fprintln(u.Stderr, s)
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}
