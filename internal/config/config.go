// Package config loads and merges the ai-session-to-md configuration with
// the precedence flag > environment > config file > built-in default, and
// validates the result. All failures are usage errors (exit code 2).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrUsage marks a configuration problem; the CLI maps it to exit code 2.
var ErrUsage = errors.New("config error")

// Env reads one environment variable; injectable for tests.
type Env func(string) string

// Dirs are the per-harness storage dirs after flag/file override; empty
// means "fall back to the platform default".
type Dirs struct {
	Claude   string
	Codex    string
	OpenCode string
}

// Config is the merged, validated configuration.
type Config struct {
	BaseURL          string
	Model            string
	Thinking         string
	ThinkingStyle    string
	APIKeyEnv        string
	MaxInputTokens   int
	MaxOutputTokens  int
	Timeout          time.Duration
	PromptAppend     string
	PromptFile       string
	SystemPromptFile string
	RedactPatterns   []string
	ExtraBody        string
	Dirs             Dirs
}

// LoadOptions carries the inputs to Load.
type LoadOptions struct {
	// Values holds set command-line flags by canonical name, in string form.
	Values map[string]string
	// Env reads the process environment.
	Env Env
	// RequireModel makes a missing model an error (--raw needs none).
	RequireModel bool
}

// envNames maps flag/config names to the AISMD_* env overrides. The
// "api-key-env" entry names an env-var *name*, not a credential.
var envNames = map[string]string{ //nolint:gosec // G101 false positive: values are env var names, not secrets
	"base-url":         "AISMD_BASE_URL",
	"model":            "AISMD_MODEL",
	"thinking":         "AISMD_THINKING",
	"thinking-style":   "AISMD_THINKING_STYLE",
	"api-key-env":      "AISMD_API_KEY_ENV",
	"max-input-tokens": "AISMD_MAX_INPUT_TOKENS",
	"timeout":          "AISMD_TIMEOUT",
}

// Path resolves the config file path: --config, then AISMD_CONFIG, then the
// per-OS default built by hand (never os.UserConfigDir, which would pick
// ~/Library/Application Support on macOS).
func Path(flagPath string, env Env, goos string) string {
	if flagPath != "" {
		return expandHome(flagPath)
	}
	if p := env("AISMD_CONFIG"); p != "" {
		return expandHome(p)
	}
	home, _ := os.UserHomeDir()
	if goos == "windows" {
		base := env("APPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "ai-session-to-md", "config.toml")
	}
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ai-session-to-md", "config.toml")
	}
	return filepath.Join(home, ".config", "ai-session-to-md", "config.toml")
}

// Load merges and validates the configuration.
func Load(opts LoadOptions) (*Config, error) {
	file, err := readFile(Path(opts.Values["config"], opts.Env, runtime.GOOS))
	if err != nil {
		return nil, err
	}

	c := Config{}
	if c.BaseURL, err = pickString("base-url", opts, file["base_url"]); err != nil {
		return nil, err
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.openai.com/v1"
	}
	for _, s := range []struct {
		dst  *string
		name string
		key  string
	}{
		{&c.Model, "model", "model"},
		{&c.Thinking, "thinking", "thinking"},
		{&c.ThinkingStyle, "thinking-style", "thinking_style"},
		{&c.APIKeyEnv, "api-key-env", "api_key_env"},
		{&c.PromptAppend, "prompt-append", "prompt_append"},
		{&c.PromptFile, "prompt-file", "prompt_file"},
		{&c.SystemPromptFile, "system-prompt-file", "system_prompt_file"},
	} {
		if *s.dst, err = pickString(s.name, opts, file[s.key]); err != nil {
			return nil, err
		}
	}
	raw := ""
	if raw, err = pickString("extra-body", opts, nil); err != nil {
		return nil, err
	}
	if raw == "" {
		if raw, err = extraBodyJSON(file["extra_body"]); err != nil {
			return nil, err
		}
	}
	c.ExtraBody = raw
	if c.MaxInputTokens, err = pickInt("max-input-tokens", opts, file["max_input_tokens"], 100000); err != nil {
		return nil, err
	}
	if c.MaxOutputTokens, err = pickInt("max-output-tokens", opts, file["max_output_tokens"], 0); err != nil {
		return nil, err
	}
	if c.Timeout, err = pickDuration("timeout", opts, file["timeout"], 10*time.Minute); err != nil {
		return nil, err
	}
	if c.RedactPatterns, err = redactPatterns(file["redact_patterns"]); err != nil {
		return nil, err
	}
	if c.Dirs, err = pickDirs(opts, file["dirs"]); err != nil {
		return nil, err
	}

	expandPaths(&c)
	if err := c.validate(opts.RequireModel); err != nil {
		return nil, err
	}
	return &c, nil
}

// pickString applies the precedence chain to one string setting: flag, then
// env, then file. Empty results mean "unset".
func pickString(name string, opts LoadOptions, fileVal any) (string, error) {
	if v, ok := opts.Values[name]; ok {
		return v, nil
	}
	if envName := envNames[name]; envName != "" {
		if v := opts.Env(envName); v != "" {
			return v, nil
		}
	}
	switch v := fileVal.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("%w: %s must be a string", ErrUsage, configKey(name))
	}
}

// configKey renders a flag name as its config-file key for error messages.
func configKey(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), "_dir", "")
}

// pickInt resolves an integer setting; the file value may be a TOML integer.
func pickInt(name string, opts LoadOptions, fileVal any, def int) (int, error) {
	raw, have, err := pickRaw(name, opts, fileVal, true)
	if err != nil {
		return 0, err
	}
	if !have {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a number, got %q", ErrUsage, configKey(name), raw)
	}
	return n, nil
}

// pickDuration resolves a duration setting (default form: 10m).
func pickDuration(name string, opts LoadOptions, fileVal any, def time.Duration) (time.Duration, error) {
	raw, have, err := pickRaw(name, opts, fileVal, false)
	if err != nil {
		return 0, err
	}
	if !have {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: %s must be a duration like 10m, got %q", ErrUsage, configKey(name), raw)
	}
	return d, nil
}

// pickRaw finds the raw string of a setting: flag, then env, then file. The
// file value must be a string, or an integer when intOK.
func pickRaw(name string, opts LoadOptions, fileVal any, intOK bool) (string, bool, error) {
	if v, ok := opts.Values[name]; ok {
		return v, true, nil
	}
	if envName := envNames[name]; envName != "" {
		if v := opts.Env(envName); v != "" {
			return v, true, nil
		}
	}
	switch v := fileVal.(type) {
	case nil:
		return "", false, nil
	case string:
		return v, v != "", nil
	case int64:
		if intOK {
			return strconv.FormatInt(v, 10), true, nil
		}
		return "", false, fmt.Errorf("%w: %s must be a string", ErrUsage, configKey(name))
	default:
		return "", false, fmt.Errorf("%w: %s has an unsupported type", ErrUsage, configKey(name))
	}
}

func extraBodyJSON(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: encode extra_body: %v", ErrUsage, err)
	}
	return string(b), nil
}

func redactPatterns(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		if v == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: redact_patterns must be an array of strings", ErrUsage)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: redact_patterns must be strings", ErrUsage)
		}
		if _, err := regexp.Compile(s); err != nil {
			return nil, fmt.Errorf("%w: redact pattern %q: %v", ErrUsage, s, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func pickDirs(opts LoadOptions, fileVal any) (Dirs, error) {
	fileDirs, _ := fileVal.(map[string]any)
	if fileVal != nil && fileDirs == nil {
		return Dirs{}, fmt.Errorf("%w: [dirs] must be a table", ErrUsage)
	}
	dir := func(flagName, fileKey string) (string, error) {
		if v, ok := opts.Values[flagName]; ok {
			return v, nil
		}
		switch v := fileDirs[fileKey].(type) {
		case nil:
			return "", nil
		case string:
			return v, nil
		default:
			return "", fmt.Errorf("%w: dirs.%s must be a string", ErrUsage, fileKey)
		}
	}
	var d Dirs
	var err error
	if d.Claude, err = dir("claude-dir", "claude"); err != nil {
		return d, err
	}
	if d.Codex, err = dir("codex-dir", "codex"); err != nil {
		return d, err
	}
	if d.OpenCode, err = dir("opencode-dir", "opencode"); err != nil {
		return d, err
	}
	return d, nil
}

func expandPaths(c *Config) {
	c.PromptFile = expandHome(c.PromptFile)
	c.SystemPromptFile = expandHome(c.SystemPromptFile)
	c.Dirs.Claude = expandHome(c.Dirs.Claude)
	c.Dirs.Codex = expandHome(c.Dirs.Codex)
	c.Dirs.OpenCode = expandHome(c.Dirs.OpenCode)
}

// expandHome rewrites a leading ~ to the user's home dir.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "~\\") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(filepath.Separator)))
}

func (c *Config) validate(requireModel bool) error {
	for _, p := range c.RedactPatterns {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("%w: redact pattern %q: %v", ErrUsage, p, err)
		}
	}
	if err := validateBaseURL(c.BaseURL); err != nil {
		return err
	}
	if err := validateThinking(c.Thinking, c.ThinkingStyle); err != nil {
		return err
	}
	if c.MaxInputTokens <= 0 {
		return fmt.Errorf("%w: max_input_tokens must be positive", ErrUsage)
	}
	if c.MaxOutputTokens < 0 {
		return fmt.Errorf("%w: max_output_tokens must not be negative", ErrUsage)
	}
	if err := validateExtraBody(c.ExtraBody); err != nil {
		return err
	}
	if requireModel && strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("%w: missing model: pass --model, set AISMD_MODEL or set model = ... in the config file", ErrUsage)
	}
	return nil
}

func validateBaseURL(raw string) error {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return fmt.Errorf("%w: base URL is required", ErrUsage)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: parse base URL %q", ErrUsage, raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return fmt.Errorf("%w: base URL %q is http:// but the host is not loopback; the API key would be sent in cleartext", ErrUsage, raw)
		}
	default:
		return fmt.Errorf("%w: base URL scheme must be https (or http on loopback), got %q", ErrUsage, u.Scheme)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || strings.HasPrefix(host, "127.") || host == "::1" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

var validLevels = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
}

var validStyles = map[string]bool{
	"auto": true, "openai": true, "openrouter": true, "none": true,
}

func validateThinking(level, style string) error {
	if level != "" && !validLevels[strings.ToLower(level)] {
		return fmt.Errorf("%w: unknown thinking level %q (want off, minimal, low, medium, high or xhigh)", ErrUsage, level)
	}
	if style != "" && !validStyles[strings.ToLower(style)] {
		return fmt.Errorf("%w: unknown thinking style %q (want auto, openai, openrouter or none)", ErrUsage, style)
	}
	return nil
}

func validateExtraBody(raw string) error {
	if raw == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return fmt.Errorf("%w: extra_body must be a JSON object: %v", ErrUsage, err)
	}
	return nil
}

// APIKey resolves the API key from the environment, never from the config
// file or the command line. The default name is AISMD_API_KEY, falling back
// to OPENAI_API_KEY. A missing key is reported by the LLM client, which
// allows keyless use of loopback endpoints.
func (c *Config) APIKey(env Env) string {
	name := c.APIKeyEnv
	if name == "" {
		if env("AISMD_API_KEY") != "" {
			name = "AISMD_API_KEY"
		} else {
			name = "OPENAI_API_KEY"
		}
	}
	return env(name)
}

// readFile parses the config file; a missing file is fine, a broken one is
// a usage error.
func readFile(path string) (map[string]any, error) {
	if path == "" {
		return map[string]any{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("%w: read config file %s: %v", ErrUsage, path, err)
	}
	doc, err := parseTOML(string(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUsage, path, err)
	}
	return doc, nil
}
