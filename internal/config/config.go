// Package config loads and merges the ai-session-to-md configuration with
// the precedence flag > environment > config file > built-in default, and
// validates the result. All failures are usage errors (exit code 2).
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/llm"
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
	path := Path(opts.Values["config"], opts.Env, runtime.GOOS)
	file, err := readFile(path)
	if err != nil {
		return nil, err
	}

	c := Config{
		BaseURL:          cmp.Or(pickString("base-url", opts, file.BaseURL), "https://api.openai.com/v1"),
		Model:            pickString("model", opts, file.Model),
		Thinking:         pickString("thinking", opts, file.Thinking),
		ThinkingStyle:    pickString("thinking-style", opts, file.ThinkingStyle),
		APIKeyEnv:        pickString("api-key-env", opts, file.APIKeyEnv),
		PromptAppend:     pickString("prompt-append", opts, file.PromptAppend),
		PromptFile:       pickString("prompt-file", opts, file.PromptFile),
		SystemPromptFile: pickString("system-prompt-file", opts, file.SystemPromptFile),
	}
	if raw := pickString("extra-body", opts, ""); raw != "" {
		c.ExtraBody = raw
	} else {
		c.ExtraBody, err = extraBodyJSON(file.ExtraBody)
		if err != nil {
			return nil, err
		}
	}
	if c.MaxInputTokens, err = pickInt("max-input-tokens", opts, file.MaxInputTokens, 100000); err != nil {
		return nil, err
	}
	if c.MaxOutputTokens, err = pickInt("max-output-tokens", opts, file.MaxOutputTokens, 0); err != nil {
		return nil, err
	}
	if c.Timeout, err = pickDuration("timeout", opts, file.Timeout, 10*time.Minute); err != nil {
		return nil, err
	}
	if c.RedactPatterns, err = redactPatterns(file.RedactPatterns); err != nil {
		return nil, err
	}
	if c.Dirs, err = pickDirs(opts, file.Dirs); err != nil {
		return nil, err
	}

	expandPaths(&c)
	if err := c.validate(opts.RequireModel, path); err != nil {
		return nil, err
	}
	return &c, nil
}

// pickString applies the precedence chain to one string setting: flag, then
// env, then file. Empty results mean "unset".
func pickString(name string, opts LoadOptions, fileVal string) string {
	if v, ok := opts.Values[name]; ok {
		return v
	}
	if envName := envNames[name]; envName != "" {
		if v := opts.Env(envName); v != "" {
			return v
		}
	}
	return fileVal
}

// configKey renders a flag name as its config-file key for error messages.
func configKey(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), "_dir", "")
}

// pickInt resolves an integer setting; the file value is already typed by the
// TOML decoder.
func pickInt(name string, opts LoadOptions, fileVal *int64, def int) (int, error) {
	if v, ok := opts.Values[name]; ok {
		return parseIntSetting(name, v)
	}
	if envName := envNames[name]; envName != "" {
		if v := opts.Env(envName); v != "" {
			return parseIntSetting(name, v)
		}
	}
	if fileVal != nil {
		return int(*fileVal), nil
	}
	return def, nil
}

func parseIntSetting(name, raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a number, got %q", ErrUsage, configKey(name), raw)
	}
	return n, nil
}

// pickDuration resolves a duration setting (default form: 10m).
func pickDuration(name string, opts LoadOptions, fileVal string, def time.Duration) (time.Duration, error) {
	raw, have := "", false
	if v, ok := opts.Values[name]; ok {
		raw, have = v, true
	} else {
		if envName := envNames[name]; envName != "" {
			if v := opts.Env(envName); v != "" {
				raw, have = v, true
			}
		}
		if !have && fileVal != "" {
			raw, have = fileVal, true
		}
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

func extraBodyJSON(v map[string]any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: encode extra_body: %v", ErrUsage, err)
	}
	return string(b), nil
}

func redactPatterns(patterns []string) ([]string, error) {
	out := make([]string, 0, len(patterns))
	for _, s := range patterns {
		if _, err := regexp.Compile(s); err != nil {
			return nil, fmt.Errorf("%w: redact pattern %q: %v", ErrUsage, s, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func pickDirs(opts LoadOptions, fileDirs dirsFile) (Dirs, error) {
	dir := func(flagName, fileVal string) string {
		return cmp.Or(pickString(flagName, opts, ""), fileVal)
	}
	return Dirs{
		Claude:   dir("claude-dir", fileDirs.Claude),
		Codex:    dir("codex-dir", fileDirs.Codex),
		OpenCode: dir("opencode-dir", fileDirs.OpenCode),
	}, nil
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

func (c *Config) validate(requireModel bool, cfgPath string) error {
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
		return fmt.Errorf("%w: missing model: pass --model or --raw, set AISMD_MODEL, or set model = ... in the config file (%s)", ErrUsage, cfgPath)
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
		if !llm.IsLoopbackHost(u.Hostname()) {
			return fmt.Errorf("%w: base URL %q is http:// but the host is not loopback; the API key would be sent in cleartext", ErrUsage, raw)
		}
	default:
		return fmt.Errorf("%w: base URL scheme must be https (or http on loopback), got %q", ErrUsage, u.Scheme)
	}
	return nil
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
func readFile(path string) (fileConfig, error) {
	if path == "" {
		return fileConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("%w: read config file %s: %v", ErrUsage, path, err)
	}
	doc, err := parseTOML(string(data))
	if err != nil {
		return fileConfig{}, fmt.Errorf("%w: %s: %v", ErrUsage, path, err)
	}
	return doc, nil
}
