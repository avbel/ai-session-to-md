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
	active atomic.Bool
}

// Step starts a progress step; call Stop when it finishes. Update changes
// the text while running (e.g. "summarizing chunk 2").
func (u *UI) Step(text string) *Spinner {
	s := &Spinner{u: u, stop: make(chan struct{}), done: make(chan struct{}), text: text}
	switch {
	case u.Quiet:
		s.still = true
	case u.ErrTTY:
		s.tty = true
		s.active.Store(true)
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
			_, _ = fmt.Fprintf(s.u.Stderr, "\r%s %s", frameGlyph(frame), text)
			frame++
		}
	}
}

func frameGlyph(i int) string {
	return string([]rune(spinnerFrames)[i%len([]rune(spinnerFrames))])
}

// Update replaces the step text.
func (s *Spinner) Update(text string) {
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}

// Stop ends the step, erasing the spinner line on a TTY. Idempotent.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		if !s.tty {
			close(s.done)
			return
		}
		close(s.stop)
		<-s.done
		s.active.Store(false)
		s.mu.Lock()
		width := max(len([]rune(s.text))+4, 12)
		s.mu.Unlock()
		_, _ = fmt.Fprintf(s.u.Stderr, "\r%s\r", strings.Repeat(" ", width))
	})
}

// Final prints the closing status line:
//
//	✓ wrote CODEX.md (12.3 KB) · 45k→3k tokens · redacted 2 secrets
//
// In quiet mode only the path (or "stdout") is printed.
func (u *UI) Final(path string, size int, tokensBefore, tokensAfter, redacted int) {
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
	if redacted > 0 {
		parts = append(parts, fmt.Sprintf("redacted %d secrets", redacted))
	}
	u.printLine(strings.Join(parts, " · "))
}

// Warn prints one warning line as "⚠ …"; suppressed in quiet mode.
func (u *UI) Warn(msg string) {
	if u.Quiet {
		return
	}
	u.printLine(u.yellow("⚠ " + msg))
}

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
