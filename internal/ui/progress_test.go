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
	u.Final("CODEX.md", 12600, 45000, 3000, 2)
	out := sink.String()
	for _, want := range []string{"✓ wrote CODEX.md (12.3 KB)", "45k→3k tokens", "redacted 2 secrets"} {
		if !strings.Contains(out, want) {
			t.Errorf("final line %q missing %q", out, want)
		}
	}
}

func TestFinalStdoutMode(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{})
	u.Final("-", 10, 100, 50, 0)
	if out := sink.String(); !strings.Contains(out, "stdout") || strings.Contains(out, "redacted") {
		t.Errorf("stdout final line = %q", out)
	}
}

func TestQuietSuppressesEverythingButPath(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{Quiet: true})
	u.Final("OUT.md", 10, 100, 50, 2)
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
}
