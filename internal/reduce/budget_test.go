package reduce

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"aaaa", 1},
		{"aaaaa", 1},
		{strings.Repeat("a", 40), 10},
	}
	for _, tc := range cases {
		if got := EstimateTokens(tc.in); got != tc.want {
			t.Errorf("EstimateTokens(%d bytes) = %d, want %d", len(tc.in), got, tc.want)
		}
	}
}

func TestBudget(t *testing.T) {
	if got := Budget(100000, 8000, 4000, 12000); got != 90000 {
		t.Errorf("Budget = %d, want 90000", got)
	}
	if got := Budget(1000, 0, 0, 0); got != -3000 {
		t.Errorf("Budget with no deductions = %d, want -3000", got)
	}
}

func TestCapHeadTailShortUnchanged(t *testing.T) {
	s := "short"
	if got := CapHeadTail(s, 4000); got != s {
		t.Errorf("CapHeadTail(short) = %q, want unchanged", got)
	}
}

func TestCapHeadTailLong(t *testing.T) {
	s := strings.Repeat("h", 600) + strings.Repeat("t", 600)
	got := CapHeadTail(s, 200)
	if len(got) != 200 {
		t.Fatalf("capped length = %d, want 200", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("capped string must stay valid UTF-8")
	}
	if !strings.HasPrefix(got, strings.Repeat("h", 90)) {
		t.Errorf("head not kept: %q", got[:30])
	}
	if !strings.HasSuffix(got, strings.Repeat("t", 90)) {
		t.Errorf("tail not kept: %q", got[len(got)-30:])
	}
	if !strings.Contains(got, textCutMarker) {
		t.Errorf("missing cut marker: %q", got)
	}
}

func TestCapHeadTailTinyMax(t *testing.T) {
	s := strings.Repeat("x", 100)
	if got := CapHeadTail(s, 5); got != "xxxxx" {
		t.Errorf("CapHeadTail(max 5) = %q, want a plain 5-byte cut", got)
	}
	if got := CapHeadTail(s, 0); got != "" {
		t.Errorf("CapHeadTail(max 0) = %q, want empty", got)
	}
}

func TestCapHeadTailMultibyte(t *testing.T) {
	s := strings.Repeat("é", 100)
	got := CapHeadTail(s, 10)
	if !utf8.ValidString(got) {
		t.Errorf("cut across a rune: %q", got)
	}
}
