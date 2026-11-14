package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/avbel/ai-session-to-md/internal/harness"
	"github.com/avbel/ai-session-to-md/internal/session"
)

func TestDefaultAdaptersSkipsMissingDirs(t *testing.T) {
	base := t.TempDir()
	claude := filepath.Join(base, "claude")
	opencode := filepath.Join(base, "opencode")
	for _, d := range []string{claude, opencode} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := DefaultAdapters(Dirs{
		Claude:   claude,
		Codex:    filepath.Join(base, "missing"),
		OpenCode: opencode,
	}, nil)

	if len(got) != 2 {
		t.Fatalf("want 2 adapters, got %d", len(got))
	}
	if got[0].Name() != session.HarnessClaudeCode {
		t.Errorf("first adapter = %q, want claude-code", got[0].Name())
	}
	if got[1].Name() != session.HarnessOpenCode {
		t.Errorf("second adapter = %q, want opencode", got[1].Name())
	}
}

func TestDefaultAdaptersAllMissing(t *testing.T) {
	base := t.TempDir()
	got := DefaultAdapters(Dirs{
		Claude:   filepath.Join(base, "no-claude"),
		Codex:    filepath.Join(base, "no-codex"),
		OpenCode: filepath.Join(base, "no-opencode"),
	}, func(string) {})

	if len(got) != 0 {
		t.Errorf("missing dirs must be skipped silently, got %d adapters", len(got))
	}
}

func TestDefaultAdaptersWiresWarn(t *testing.T) {
	base := t.TempDir()
	var warns []string
	got := DefaultAdapters(Dirs{Codex: base}, func(w string) { warns = append(warns, w) })

	if len(got) != 1 || got[0].Name() != session.HarnessCodex {
		t.Fatalf("want the codex adapter only, got %d", len(got))
	}
	if _, err := got[0].List(t.Context(), harness.ListOptions{}); err != nil {
		t.Errorf("an empty codex dir should list cleanly through the fallback: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}
