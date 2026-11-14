package ui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// spinnerFrames are the braille animation frames.
const spinnerFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

const spinnerTick = 90 * time.Millisecond

// spinTick is the tick used by the animation loop; tests shrink it.
var spinTick = spinnerTick

// spinnerReserve reserves cells for the frame glyph, the separator and the
// possible elapsed suffix when clipping step text to the terminal width.
const spinnerReserve = 10

// Spinner is one progress step. On a TTY it animates a line on stderr; in
// non-TTY mode it prints the step once; in quiet mode it says nothing.
type Spinner struct {
	u      *UI
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	text   string
	still  bool
	tty    bool
	start  time.Time
	active atomic.Bool
}

// Step starts a progress step; call Stop when it finishes. Update changes
// the text while running (e.g. "summarizing chunk 2").
func (u *UI) Step(text string) *Spinner {
	s := &Spinner{u: u, stop: make(chan struct{}), done: make(chan struct{}), text: text, start: time.Now()}
	switch {
	case u.Quiet:
		s.still = true
	case u.ErrTTY:
		s.tty = true
		s.active.Store(true)
		u.spins.Add(1)
		go s.animate()
	default:
		s.still = true
		u.printLine(text + "…")
	}
	return s
}

func (s *Spinner) animate() {
	defer close(s.done)
	frame := 0
	for {
		select {
		case <-s.stop:
			return
		case <-time.After(spinTick):
			s.mu.Lock()
			text := s.text
			s.mu.Unlock()
			_, _ = fmt.Fprintf(s.u.Stderr, "\r\x1b[2K%s %s%s",
				frameGlyph(frame), s.clip(text), s.elapsed())
			frame++
		}
	}
}

func frameGlyph(i int) string {
	return string([]rune(spinnerFrames)[i%len([]rune(spinnerFrames))])
}

// clip keeps the step text inside the terminal width, leaving room for the
// frame and the elapsed suffix.
func (s *Spinner) clip(text string) string {
	return truncCells(text, max(s.u.width-spinnerReserve, 10))
}

// elapsed is appended to the step text after a few seconds, e.g. " · 2m14s".
func (s *Spinner) elapsed() string {
	d := time.Since(s.start)
	if d < 5*time.Second {
		return ""
	}
	return " · " + fmtDuration(d)
}

func fmtDuration(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

// Update replaces the step text.
func (s *Spinner) Update(text string) {
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}

// Stop ends the step, clearing the spinner line on a TTY. Idempotent.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		if s.tty {
			s.u.spins.Add(-1)
			close(s.stop)
			<-s.done
			s.active.Store(false)
			_, _ = fmt.Fprint(s.u.Stderr, "\r\x1b[2K")
			return
		}
		close(s.done)
	})
}

// Redactions summarizes what the redactor counted, for the final line.
type Redactions struct {
	Total int
	// Kinds is the redaction kind names, most frequent first.
	Kinds []string
}

// Final prints the closing status line:
//
//	✓ wrote CODEX.md (12.3 KB) · 45k→3k tokens · redacted 2 secrets (aws_key, jwt)
//
// In quiet mode only the path (or "stdout") is printed.
func (u *UI) Final(path string, size int, tokensBefore, tokensAfter int, red Redactions) {
	if u.Quiet {
		u.printLine(path)
		return
	}
	var parts []string
	if path == "-" {
		parts = append(parts, u.green("✓ wrote handoff to stdout"))
	} else {
		parts = append(parts, u.green(fmt.Sprintf("✓ wrote %s (%s)", path, humanBytes(size))))
	}
	parts = append(parts, fmt.Sprintf("%s→%s tokens", humanTokens(tokensBefore), humanTokens(tokensAfter)))
	if line := redactedLine(red); line != "" {
		parts = append(parts, line)
	}
	u.printLine(strings.Join(parts, " · "))
}

// redactedLine pluralizes correctly and names the kinds when there are only
// a few.
func redactedLine(red Redactions) string {
	if red.Total <= 0 {
		return ""
	}
	unit := "secrets"
	if red.Total == 1 {
		unit = "secret"
	}
	line := fmt.Sprintf("redacted %d %s", red.Total, unit)
	if len(red.Kinds) > 0 && len(red.Kinds) <= 3 {
		line += " (" + strings.Join(red.Kinds, ", ") + ")"
	}
	return line
}

// Warn prints one warning line as "⚠ …"; suppressed in quiet mode.
func (u *UI) Warn(msg string) {
	if u.Quiet {
		return
	}
	u.printLine(u.yellow("⚠ " + msg))
}

// Fail prints a failure line as "✗ msg" (red ✗ on a color TTY). Errors are
// printed in quiet mode too.
func (u *UI) Fail(msg string) {
	u.printLine(u.red("✗ " + msg))
}

// Hint prints an indented follow-up suggestion under a failure line.
func (u *UI) Hint(text string) {
	u.printLine(u.dim("  hint: " + text))
}

// Width is the terminal width the UI lays out to; 100 when unknown.
func (u *UI) Width() int { return u.width }

// humanBytes renders a size like "12.3 KB".
func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// humanTokens renders a token estimate like "45k".
func humanTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%dk", (n+500)/1000)
}
