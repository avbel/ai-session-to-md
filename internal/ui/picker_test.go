package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "1m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{3 * time.Hour, "3h"},
		{23 * time.Hour, "23h"},
		{2 * 24 * time.Hour, "2d"},
		{9 * 24 * time.Hour, "9d"},
	}
	for _, tt := range tests {
		if got := FormatAge(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("FormatAge(-%v) = %q, want %q", tt.ago, got, tt.want)
		}
	}
	if got := FormatAge(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), now); got != "Nov 02" {
		t.Errorf("old date = %q, want Nov 02", got)
	}
	if got := FormatAge(time.Time{}, now); got != "now" {
		t.Errorf("zero time = %q", got)
	}
	if got := FormatAge(now.Add(time.Minute), now); got != "now" {
		t.Errorf("future = %q", got)
	}
}

func TestMiddleTruncate(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10ch", 11, "exactly10ch"},
	}
	// The third case is built to be readable; verify by shape instead.
	for _, tt := range tests[:2] {
		if got := middleTruncate(tt.in, tt.max); got != tt.in {
			t.Errorf("middleTruncate(%q, %d) = %q", tt.in, tt.max, got)
		}
	}
	if got := middleTruncate("abcdefghij", 5); got != "ab…ij" {
		t.Errorf("middleTruncate = %q, want ab…ij", got)
	}
	if got := middleTruncate("abc", 1); got != "…" {
		t.Errorf("max=1 = %q", got)
	}
	if got := middleTruncate("日本語テスト", 3); got != "日…ト" {
		t.Errorf("runes = %q", got)
	}
}

func TestTruncRight(t *testing.T) {
	if got := truncRight("abcdefgh", 5); got != "abcd…" {
		t.Errorf("truncRight = %q", got)
	}
	if got := truncRight("abc", 5); got != "abc" {
		t.Errorf("short = %q", got)
	}
	if got := truncRight("abcdef", 0); got != "" {
		t.Errorf("zero = %q", got)
	}
}

func TestRelHome(t *testing.T) {
	home := "/home/u"
	tests := []struct{ in, want string }{
		{"/home/u/proj", "~/proj"},
		{"/home/u", "~"},
		{"/other/path", "/other/path"},
		{"", "-"},
	}
	for _, tt := range tests {
		if got := relHome(tt.in, home); got != tt.want {
			t.Errorf("relHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatRow(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	m := session.Meta{
		Harness:   session.HarnessCodex,
		ID:        "abc123def456",
		Title:     "Fix the reducer budget overflow",
		Cwd:       "/home/u/proj/sub",
		Branch:    "main",
		UpdatedAt: now.Add(-3 * time.Hour),
	}
	row := FormatRow(m, now, "/home/u", 60)
	for _, want := range []string{"3h", "codex", "~/proj/sub", "main", "Fix the reducer budget overflow"} {
		if !strings.Contains(row, want) {
			t.Errorf("row %q missing %q", row, want)
		}
	}
	if !strings.HasPrefix(row, "3h") {
		t.Errorf("row %q does not start with the age column", row)
	}
}

func TestFormatRowsTruncatesToWidth(t *testing.T) {
	now := time.Now()
	var metas []session.Meta
	for range 30 {
		metas = append(metas, session.Meta{
			Harness:   session.HarnessOpenCode,
			ID:        strings.Repeat("x", 40),
			Title:     strings.Repeat("t", 200),
			Cwd:       "/home/u/" + strings.Repeat("p", 60),
			Branch:    strings.Repeat("b", 30),
			UpdatedAt: now,
		})
	}
	rows := FormatRows(metas, now, "/home/u", 80)
	for i, row := range rows {
		if n := len([]rune(row)); n > 80 {
			t.Errorf("row %d is %d runes wide: %q", i, n, row)
		}
	}
}

func TestFormatRowsMinimalWidth(t *testing.T) {
	now := time.Now()
	metas := []session.Meta{{Harness: "codex", Title: "hello", UpdatedAt: now}}
	rows := FormatRows(metas, now, "", 20)
	if len(rows) != 1 || !strings.Contains(rows[0], "hello") {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestNewColorRules(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, true, Options{Env: func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	}})
	if u.Color {
		t.Error("NO_COLOR must disable color")
	}
	u = New(&sink, nil, true, false, Options{})
	if u.Color {
		t.Error("non-TTY stderr must disable color")
	}
	u = New(&sink, nil, true, true, Options{})
	if !u.Color {
		t.Error("TTY stderr with colors must be colored")
	}
}

func TestPickNilStdinCancelled(t *testing.T) {
	var sink strings.Builder
	u := New(&sink, nil, true, false, Options{})
	_, err := u.Pick(t.Context(), nil, "/root", time.Now())
	if err == nil || !errors.Is(err, ErrNoSessions) {
		t.Fatalf("empty list = %v, want ErrNoSessions", err)
	}
}
