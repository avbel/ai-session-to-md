package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) Env {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	_, err := Load(LoadOptions{Values: map[string]string{}, Env: envOf(nil), RequireModel: true})
	if err == nil || !strings.Contains(err.Error(), "missing model") {
		t.Fatalf("missing model = %v, want exit-2 hint", err)
	}
	c, err := Load(LoadOptions{Values: map[string]string{"model": "m"}, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.MaxInputTokens != 100000 || c.Timeout != 10*time.Minute {
		t.Errorf("defaults: %+v", c)
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	body := "base_url = \"https://file.example/v1\"\nmodel = \"file-model\"\n" +
		"timeout = \"30m\"\nmax_input_tokens = 1234\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// file only
	c, err := Load(LoadOptions{Values: map[string]string{"config": cfg}, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "file-model" || c.BaseURL != "https://file.example/v1" ||
		c.Timeout != 30*time.Minute || c.MaxInputTokens != 1234 {
		t.Fatalf("file values: %+v", c)
	}

	// env beats file
	e := envOf(map[string]string{"AISMD_MODEL": "env-model", "AISMD_TIMEOUT": "1m"})
	c, err = Load(LoadOptions{Values: map[string]string{"config": cfg}, Env: e})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "env-model" || c.Timeout != time.Minute || c.BaseURL != "https://file.example/v1" {
		t.Fatalf("env over file: %+v", c)
	}

	// flag beats env and file
	v := map[string]string{"config": cfg, "model": "flag-model", "max-input-tokens": "99"}
	c, err = Load(LoadOptions{Values: v, Env: e})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "flag-model" || c.MaxInputTokens != 99 {
		t.Fatalf("flag wins: %+v", c)
	}
}

func TestLoadAPIKeyEnvPrecedence(t *testing.T) {
	v := map[string]string{"api-key-env": "FLAG_KEY"}
	c, err := Load(LoadOptions{Values: v, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIKey(envOf(nil)) != "" {
		t.Error("missing env var must yield an empty key")
	}
	e := envOf(map[string]string{"FLAG_KEY": "flagkey"})
	if got := c.APIKey(e); got != "flagkey" {
		t.Errorf("APIKey = %q", got)
	}

	// default names: AISMD_API_KEY wins over OPENAI_API_KEY
	c2, err := Load(LoadOptions{Values: map[string]string{}, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := envOf(map[string]string{"AISMD_API_KEY": "a", "OPENAI_API_KEY": "b"})
	if got := c2.APIKey(d); got != "a" {
		t.Errorf("default key env = %q, want AISMD_API_KEY", got)
	}
	d = envOf(map[string]string{"OPENAI_API_KEY": "b"})
	if got := c2.APIKey(d); got != "b" {
		t.Errorf("fallback key env = %q", got)
	}
}

func TestConfigFileKeyIsIgnored(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("api_key = \"leaked\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(LoadOptions{Values: map[string]string{"config": cfg}, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIKeyEnv != "" || c.APIKey(envOf(nil)) != "" {
		t.Errorf("api key must never come from the config file: %+v", c)
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		wantEr string
	}{
		{"bad redact regex", "redact_patterns = ['([bad']\n", "redact pattern"},
		{"http non-loopback", "base_url = \"http://api.example.com/v1\"\n", "cleartext"},
		{"bad scheme", "base_url = \"ftp://x\"\n", "scheme"},
		{"bad thinking", "thinking = \"maximum\"\n", "thinking level"},
		{"bad style", "thinking_style = \"deep\"\n", "thinking style"},
		{"bad extra body", "extra_body = 3\n", "JSON object"},
		{"bad timeout", "timeout = \"soon\"\n", "duration"},
		{"bad int", "max_input_tokens = \"lots\"\n", "must be a number"},
		{"toml syntax", "base_url =\n", "missing value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(cfg, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(LoadOptions{Values: map[string]string{"config": cfg}, Env: envOf(nil)})
			if err == nil || !errors.Is(err, ErrUsage) || !strings.Contains(err.Error(), tt.wantEr) {
				t.Fatalf("Load error = %v, want ErrUsage containing %q", err, tt.wantEr)
			}
		})
	}
}

func TestLoadMissingModelHint(t *testing.T) {
	_, err := Load(LoadOptions{Values: map[string]string{}, Env: envOf(nil), RequireModel: true})
	if err == nil || !errors.Is(err, ErrUsage) ||
		!strings.Contains(err.Error(), "--model") || !strings.Contains(err.Error(), "AISMD_MODEL") {
		t.Fatalf("error = %v, want hint mentioning --model and AISMD_MODEL", err)
	}
}

func TestLoopbackHTTPAllowed(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("base_url = \"http://127.0.0.1:1234/v1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(LoadOptions{Values: map[string]string{"config": cfg}, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("loopback http rejected: %v", err)
	}
	if c.BaseURL != "http://127.0.0.1:1234/v1" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
}

func TestPathPerOS(t *testing.T) {
	got := Path("", envOf(nil), "darwin")
	if !strings.HasSuffix(got, filepath.Join(".config", "ai-session-to-md", "config.toml")) {
		t.Errorf("darwin default = %q", got)
	}
	xdg := Path("", envOf(map[string]string{"XDG_CONFIG_HOME": "/xdg"}), "linux")
	if xdg != filepath.Join("/xdg", "ai-session-to-md", "config.toml") {
		t.Errorf("xdg = %q", xdg)
	}
	win := Path("", envOf(map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}), "windows")
	if win != filepath.Join(`C:\Users\u\AppData\Roaming`, "ai-session-to-md", "config.toml") {
		t.Errorf("windows = %q", win)
	}
	if p := Path("~/cfg.toml", envOf(nil), "darwin"); strings.Contains(p, "~") {
		t.Errorf("flag path not expanded: %q", p)
	}
	if p := Path("/explicit.toml", envOf(map[string]string{"AISMD_CONFIG": "/env.toml"}), "darwin"); p != "/explicit.toml" {
		t.Errorf("flag beats env: %q", p)
	}
	if p := Path("", envOf(map[string]string{"AISMD_CONFIG": "/env.toml"}), "darwin"); p != "/env.toml" {
		t.Errorf("env path = %q", p)
	}
}

func TestDirsFromFileAndFlags(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	body := "[dirs]\nclaude = \"~/claude-store\"\ncodex = \"/codex\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(LoadOptions{Values: map[string]string{"config": cfg, "opencode-dir": "/oc"}, Env: envOf(nil)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasPrefix(c.Dirs.Claude, "/") || strings.Contains(c.Dirs.Claude, "~") {
		t.Errorf("dirs.claude not expanded: %q", c.Dirs.Claude)
	}
	if c.Dirs.Codex != "/codex" || c.Dirs.OpenCode != "/oc" {
		t.Errorf("dirs: %+v", c.Dirs)
	}
}

func TestExpandTilde(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	if got := expandHome("~"); got != "/home/u" {
		t.Errorf("expandHome(~) = %q", got)
	}
	if got := expandHome("~/sub"); got != filepath.Join("/home/u", "sub") {
		t.Errorf("expandHome(~/sub) = %q", got)
	}
	if got := expandHome("/abs"); got != "/abs" {
		t.Errorf("expandHome(/abs) = %q", got)
	}
}
