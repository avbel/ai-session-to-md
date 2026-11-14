package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// lipglossWidth measures a string in display cells.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

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

func TestTruncCells(t *testing.T) {
	if got := truncCells("abcdefgh", 5); got != "abcd…" {
		t.Errorf("truncCells = %q, want abcd…", got)
	}
	if got := truncCells("abc", 5); got != "abc" {
		t.Errorf("short = %q, want abc", got)
	}
	if got := truncCells("abcdef", 0); got != "" {
		t.Errorf("zero = %q", got)
	}
	if got := truncCells("abc", 1); got != "…" {
		t.Errorf("max=1 = %q, want …", got)
	}
	// Cells, not runes: two wide runes plus the ellipsis are 3 cells.
	if got := truncCells("日本語", 3); got != "日…" {
		t.Errorf("wide = %q, want 日…", got)
	}
	if w := lipglossWidth(truncCells("日本語", 3)); w > 3 {
		t.Errorf("wide truncation is %d cells wide", w)
	}
}

func TestMiddleCells(t *testing.T) {
	if got := middleCells("abcdefghij", 5); got != "ab…ij" {
		t.Errorf("middleCells = %q, want ab…ij", got)
	}
	if got := middleCells("abc", 1); got != "…" {
		t.Errorf("max=1 = %q", got)
	}
	if got := middleCells("日本語テスト", 3); got != "…ト" {
		t.Errorf("runes = %q, want …ト", got)
	}
}

func TestMiddleTruncatePathKeepsLastSegment(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"~/Projects/api", 12, "~/Proje…/api"},
		{"~/Projects/x/y/ai-session-to-md", 20, "~/…/ai-session-to-md"},
		{"short", 10, "short"},
	}
	for _, tt := range tests {
		if got := middleTruncatePath(tt.in, tt.max); got != tt.want {
			t.Errorf("middleTruncatePath(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
}

func TestRelHome(t *testing.T) {
	home := t.TempDir()
	tests := []struct{ in, want string }{
		{filepath.Join(home, "proj"), "~/proj"},
		{home, "~"},
		{"/other/path", "/other/path"},
		{"", "-"},
	}
	for _, tt := range tests {
		if got := RelHome(tt.in, home); got != tt.want {
			t.Errorf("RelHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// A same-prefix longer name must not fold: home "u" in "users" stays.
	if got := RelHome(home+"x/v", home); got != home+"x/v" {
		t.Errorf("RelHome(%q) = %q, want unchanged", home+"x/v", got)
	}
}

func TestFormatRow(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	m := session.Meta{
		Harness:   session.HarnessCodex,
		ID:        "abc123def456",
		Title:     "Fix the reducer budget overflow",
		Cwd:       filepath.Join(home, "proj", "sub"),
		Branch:    "main",
		UpdatedAt: now.Add(-3 * time.Hour),
	}
	other := m
	other.Cwd = filepath.Join(home, "other")
	layout := newPickLayout([]session.Meta{m, other}, home, home, 80)
	row := layout.row(m, now)
	for _, want := range []string{"3h", "codex", "proj/sub", "main", "Fix the reducer budget overflow"} {
		if !strings.Contains(row, want) {
			t.Errorf("row %q missing %q", row, want)
		}
	}
	if !strings.HasPrefix(row, "3h") {
		t.Errorf("row %q does not start with the age column", row)
	}
	// The harness column uses the short name: "codex" fits, "claude-code" would not.
	if !strings.Contains(row, "codex") {
		t.Errorf("row %q missing the short harness name", row)
	}
}

func TestPickLayoutHidesSharedProject(t *testing.T) {
	now := time.Now()
	metas := []session.Meta{
		{Harness: "codex", Title: "one", Cwd: "/home/u/proj", UpdatedAt: now},
		{Harness: "codex", Title: "two", Cwd: "/home/u/proj", UpdatedAt: now},
	}
	l := newPickLayout(metas, "/home/u", "/home/u/proj", 80)
	if l.showProject {
		t.Error("project column must be hidden when every row shares the root")
	}
	if strings.Contains(l.header(), "project") {
		t.Errorf("header %q has a project column", l.header())
	}
	rows := l.rows(metas, now)
	for i, row := range rows {
		if !strings.Contains(row, metas[i].Title) {
			t.Errorf("row %d %q missing its title", i, row)
		}
	}
}

func TestPickLayoutShowsMixedProjects(t *testing.T) {
	now := time.Now()
	metas := []session.Meta{
		{Harness: "codex", Title: "one", Cwd: "/home/u/proj", UpdatedAt: now},
		{Harness: "codex", Title: "two", Cwd: "/home/u/other", UpdatedAt: now},
	}
	l := newPickLayout(metas, "/home/u", "/home/u", 80)
	if !l.showProject {
		t.Error("project column must show when rows differ")
	}
	header := l.header()
	if !strings.Contains(header, "project") {
		t.Errorf("header %q has no project column", header)
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
	l := newPickLayout(metas, "/home/u", "/home/u", 80)
	for i, row := range l.rows(metas, now) {
		if n := lipglossWidth(row); n > 80 {
			t.Errorf("row %d is %d cells wide: %q", i, n, row)
		}
	}
}

func TestFormatRowsMinimalWidth(t *testing.T) {
	now := time.Now()
	metas := []session.Meta{{Harness: "codex", Title: "hello", UpdatedAt: now}}
	l := newPickLayout(metas, "", "", 20)
	rows := l.rows(metas, now)
	if len(rows) != 1 || !strings.Contains(rows[0], "hello") {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestFormatMatches(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	metas := []session.Meta{
		{Harness: session.HarnessClaudeCode, ID: "dupsess-aaaaaa", Title: "first duplicate session", Branch: "main", UpdatedAt: now.Add(-time.Hour)},
		{Harness: session.HarnessCodex, ID: "dupsess-bbbbbb", Title: "second duplicate session", Branch: "dev", UpdatedAt: now},
	}
	rows := FormatMatches(metas, now, 100)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for i, want := range []string{"dupsess-aaaaaa", "dupsess-bbbbbb"} {
		if !strings.Contains(rows[i], want) {
			t.Errorf("row %d %q missing id %q", i, rows[i], want)
		}
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
	_, err := u.Pick(t.Context(), nil, "/root", false, time.Now())
	if err == nil || !errors.Is(err, ErrNoSessions) {
		t.Fatalf("empty list = %v, want ErrNoSessions", err)
	}
}
