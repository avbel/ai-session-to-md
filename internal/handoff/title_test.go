package handoff

import (
	"strings"
	"testing"
)

func TestCleanTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain", "Fix the reader", 80, "Fix the reader"},
		{"first non-empty line", "\n  \nsecond line\nthird", 80, "second line"},
		{"whitespace collapsed", "a\t b\n  c\r\nd", 80, "a b"},
		{"tab-only first line falls to next", "\t\nreal line", 80, "real line"},
		{"control and ESC stripped", "a\x1b[31mb\x07c\x00d", 80, "a[31mbcd"},
		{"capped at runes", strings.Repeat("é", 60), 40, strings.Repeat("é", 40)},
		{"only whitespace", "   \n\t\n", 80, ""},
		{"empty", "", 80, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanTitle(tc.in, tc.max); got != tc.want {
				t.Errorf("CleanTitle(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}
