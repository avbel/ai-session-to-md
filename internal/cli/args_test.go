package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseInterspersed(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		check   func(*testing.T, *Flags)
		wantErr bool
	}{
		{
			name: "flags after positionals",
			args: []string{"codex", "abc123xyz", "--latest", "--raw"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "codex" || f.SessionID != "abc123xyz" || !f.Latest || !f.Raw {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "flags before positionals",
			args: []string{"--latest", "--raw", "cx", "abc123xyz"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "codex" || f.SessionID != "abc123xyz" || !f.Latest || !f.Raw {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "flags between positionals",
			args: []string{"cc", "--all", "abc123xyz", "--limit", "5"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "claude-code" || f.SessionID != "abc123xyz" || !f.All || f.Limit != 5 {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "aliases",
			args: []string{"claude"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "claude-code" {
					t.Errorf("got %q", f.Harness)
				}
			},
		},
		{
			name: "short output",
			args: []string{"oc", "-o", "-", "--force"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "opencode" || f.Output != "-" || !f.Force {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "antigravity alias and dir flag",
			args: []string{"agy", "--antigravity-dir", "/g"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "antigravity" || f.AntigravityDir != "/g" {
					t.Errorf("got %+v", f)
				}
				if v := f.Values()["antigravity-dir"]; v != "/g" {
					t.Errorf("Values()[antigravity-dir] = %q", v)
				}
			},
		},
		{
			name: "long output with dir",
			args: []string{"--output", "out/hand.md", "codex"},
			check: func(t *testing.T, f *Flags) {
				if f.Harness != "codex" || f.Output != "out/hand.md" {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name: "no positionals",
			args: []string{"--latest"},
			check: func(t *testing.T, f *Flags) {
				if !f.Latest || f.Harness != "" || f.SessionID != "" {
					t.Errorf("got %+v", f)
				}
			},
		},
		{
			name:    "unknown flag",
			args:    []string{"--nope", "codex"},
			wantErr: true,
		},
		{
			name:    "bad session id",
			args:    []string{"codex", "short"},
			wantErr: true,
		},
		{
			name:    "dashed session id",
			args:    []string{"-not-a-flag-like-id"},
			wantErr: true,
		},
		{
			name:    "third positional",
			args:    []string{"codex", "abc123xyz", "def456ghi"},
			wantErr: true,
		},
		{
			name:    "two harnesses",
			args:    []string{"codex", "claude"},
			wantErr: true,
		},
		{
			name:    "session id then harness",
			args:    []string{"abc123xyz", "codex"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%v) = %+v, want error", tt.args, got)
				}
				if !errors.Is(err, ErrUsage) {
					t.Fatalf("Parse(%v) error %v, want ErrUsage", tt.args, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%v): %v", tt.args, err)
			}
			if got.Limit != DefaultLimit && !slices.Contains(tt.args, "--limit") {
				t.Fatalf("default limit lost: %+v", got)
			}
			tt.check(t, got)
		})
	}
}

func TestParseDefaults(t *testing.T) {
	f, err := Parse([]string{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Limit != DefaultLimit {
		t.Errorf("Limit = %d, want %d", f.Limit, DefaultLimit)
	}
	if f.MaxInputTokens != DefaultMaxInputTokens {
		t.Errorf("MaxInputTokens = %d, want %d", f.MaxInputTokens, DefaultMaxInputTokens)
	}
	if f.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", f.Timeout, DefaultTimeout)
	}
}

func TestValueTracking(t *testing.T) {
	f, err := Parse([]string{"--model", "m1", "-o", "-", "--timeout", "5m", "--verbose"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, ok := f.Value("model"); !ok || v != "m1" {
		t.Errorf("model = %q, %v", v, ok)
	}
	if v, ok := f.Value("output"); !ok || v != "-" {
		t.Errorf("output = %q, %v", v, ok)
	}
	if _, ok := f.Value("base-url"); ok {
		t.Error("unset base-url reported as set")
	}
	if _, ok := f.Value("verbose"); ok {
		t.Error("non-config flag reported as config value")
	}
	vals := f.Values()
	if vals["timeout"] != (5 * time.Minute).String() {
		t.Errorf("timeout value = %q", vals["timeout"])
	}
	if _, ok := vals["output"]; ok {
		t.Error("output is not a config setting but appears in Values")
	}
}

func TestHelpAndVersionFlags(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--version"}} {
		f, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if !f.ShowHelp && !f.ShowVersion {
			t.Errorf("Parse(%v) set neither help nor version", args)
		}
	}
}

func TestPickDecision(t *testing.T) {
	if err := PickDecision(true, false, false); err != nil {
		t.Errorf("with id: %v", err)
	}
	if err := PickDecision(false, true, false); err != nil {
		t.Errorf("with latest: %v", err)
	}
	if err := PickDecision(false, false, true); err != nil {
		t.Errorf("with tty: %v", err)
	}
	err := PickDecision(false, false, false)
	if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), "no TTY") {
		t.Errorf("no TTY decision = %v, want usage error mentioning no TTY", err)
	}
}

func TestOverwriteDecision(t *testing.T) {
	if err := OverwriteDecision(OverwriteAbsent, false, false); err != nil {
		t.Errorf("absent: %v", err)
	}
	if err := OverwriteDecision(OverwriteExists, true, false); err != nil {
		t.Errorf("force: %v", err)
	}
	if err := OverwriteDecision(OverwriteExists, false, true); err != nil {
		t.Errorf("tty confirm: %v", err)
	}
	err := OverwriteDecision(OverwriteExists, false, false)
	if err == nil || !errors.Is(err, ErrUsage) {
		t.Errorf("exists without force or tty = %v, want usage error", err)
	}
	err = OverwriteDecision(OverwriteSymlink, true, true)
	if err == nil || !errors.Is(err, ErrUsage) {
		t.Errorf("symlink = %v, want usage error even with force", err)
	}
}
