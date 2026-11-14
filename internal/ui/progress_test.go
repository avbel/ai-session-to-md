package ui

import (
	"strings"
	"testing"
	"time"
)

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{512, "512 B"},
		{12 * 1024, "12.0 KB"},
		{12600, "12.3 KB"},
		{2 * 1024 * 1024, "2.0 MB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHumanTokens(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{999, "999"},
		{1000, "1k"},
		{45000, "45k"},
		{3000, "3k"},
	}
	for _, tt := range tests {
		if got := humanTokens(tt.in); got != tt.want {
			t.Errorf("humanTokens(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFinalLine(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{})
	u.Final("CODEX.md", 12600, 45000, 3000, Redactions{Total: 2, Kinds: []string{"aws_key", "jwt"}})
	out := sink.String()
	for _, want := range []string{"✓ wrote CODEX.md (12.3 KB)", "45k→3k tokens", "redacted 2 secrets (aws_key, jwt)"} {
		if !strings.Contains(out, want) {
			t.Errorf("final line %q missing %q", out, want)
		}
	}
}

func TestFinalLineSingularAndManyKinds(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{})
	u.Final("CODEX.md", 10, 100, 50, Redactions{Total: 1, Kinds: []string{"aws_key"}})
	if out := sink.String(); !strings.Contains(out, "redacted 1 secret (aws_key)") || strings.Contains(out, "secrets") {
		t.Errorf("singular final line = %q", out)
	}
	sink.Reset()
	u.Final("CODEX.md", 10, 100, 50, Redactions{Total: 4, Kinds: []string{"a", "b", "c", "d"}})
	if out := sink.String(); !strings.Contains(out, "redacted 4 secrets") || strings.Contains(out, "redacted 4 secrets (") {
		t.Errorf("many-kinds final line = %q", out)
	}
}

func TestFinalStdoutMode(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{})
	u.Final("-", 10, 100, 50, Redactions{})
	if out := sink.String(); !strings.Contains(out, "stdout") || strings.Contains(out, "redacted") {
		t.Errorf("stdout final line = %q", out)
	}
}

func TestQuietSuppressesEverythingButPath(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{Quiet: true})
	u.Final("OUT.md", 10, 100, 50, Redactions{Total: 2})
	u.Warn("should not appear")
	s := u.Step("loading")
	s.Stop()
	if out := sink.String(); strings.Contains(out, "⚠") || strings.Contains(out, "✓") || strings.Contains(out, "loading") {
		t.Errorf("quiet output = %q, want only the path", out)
	}
	if !strings.Contains(sink.String(), "OUT.md") {
		t.Errorf("quiet output %q missing the path", sink.String())
	}
}

func TestWarnAndPlainStepsNonTTY(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, false, false, Options{})
	u.Warn("uncommitted changes")
	s := u.Step("Reading session")
	if s.tty {
		t.Error("non-TTY step must not animate")
	}
	s.Stop()
	for _, want := range []string{"⚠ uncommitted changes", "Reading session…"} {
		if !strings.Contains(sink.String(), want) {
			t.Errorf("output %q missing %q", sink.String(), want)
		}
	}
}

func TestSpinnerTTYAnimatesAndClears(t *testing.T) {
	old := spinTick
	spinTick = time.Millisecond
	t.Cleanup(func() { spinTick = old })
	var sink strings.Builder
	u := New(&sink, nil, true, true, Options{})
	s := u.Step("Summarizing with m…")
	time.Sleep(5 * time.Millisecond)
	s.Update("summarizing chunk 1")
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	s.Stop() // idempotent
	out := sink.String()
	if !strings.Contains(out, "Summarizing with m…") || !strings.Contains(out, "summarizing chunk 1") {
		t.Errorf("spinner output = %q", out)
	}
	if strings.Contains(out, "chunk 1") && !strings.Contains(out, "\r") {
		t.Errorf("spinner never returned to line start: %q", out)
	}
	if !strings.Contains(out, "\x1b[2K") {
		t.Errorf("spinner does not clear the line with \\r\\x1b[2K: %q", out)
	}
	if strings.Count(out, "\x1b[2K") < 2 {
		t.Errorf("Stop must clear the line too: %q", out)
	}
}

func TestSpinnerClipsToWidth(t *testing.T) {
	old := spinTick
	spinTick = time.Millisecond
	t.Cleanup(func() { spinTick = old })
	var sink strings.Builder
	u := New(&sink, nil, true, true, Options{})
	u.width = 30
	long := strings.Repeat("x", 200)
	s := u.Step(long)
	time.Sleep(10 * time.Millisecond)
	s.Stop()
	out := sink.String()
	if strings.Count(out, "xxxxx") < 1 {
		t.Errorf("spinner output missing the text: %q", head(out, 120))
	}
	for line := range strings.SplitSeq(out, "\r") {
		if strings.Contains(line, "xxxxx") && lipglossWidth(line) > 30 {
			t.Fatalf("spinner line %q is %d cells wide, over 30", line, lipglossWidth(line))
		}
	}
}

func TestSpinnerElapsedAfterFiveSeconds(t *testing.T) {
	if got := fmtDuration(45 * time.Second); got != "45s" {
		t.Errorf("fmtDuration(45s) = %q", got)
	}
	if got := fmtDuration(134 * time.Second); got != "2m14s" {
		t.Errorf("fmtDuration(134s) = %q, want 2m14s", got)
	}
	s := &Spinner{start: time.Now().Add(-134 * time.Second)}
	if got := s.elapsed(); got != " · 2m14s" {
		t.Errorf("elapsed = %q", got)
	}
	s = &Spinner{start: time.Now()}
	if got := s.elapsed(); got != "" {
		t.Errorf("fresh spinner elapsed = %q, want empty", got)
	}
}

func TestWarnClearsActiveSpinnerLine(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, true, Options{})
	u.spins.Store(1)
	u.Warn("uncommitted changes")
	if out := sink.String(); !strings.HasPrefix(out, "\r\x1b[2K") {
		t.Errorf("warning while a spinner is active = %q, want a leading clear", out)
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
