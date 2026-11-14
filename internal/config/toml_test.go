package config

import "testing"

func TestParseTOMLSchema(t *testing.T) {
	src := `
# comment
base_url = "https://openrouter.ai/api/v1"   # trailing comment
model = 'some/model'
thinking = "medium"
thinking_style = "auto"
api_key_env = "OPENROUTER_API_KEY"
max_input_tokens = 100000
max_output_tokens = 8000
timeout = "10m"
prompt_append = "Always list DB migrations separately."
prompt_file = "~/.config/ai-session-to-md/extra.md"
system_prompt_file = ""
redact_patterns = ['(?i)internal-token-[a-z0-9]{20}', 'second']
extra_body = { temperature = 0.2, top_p = 0.9, flag = true, note = "x=y" }

[dirs]
claude = "~/.claude"
codex = "~/.codex"
opencode = "~/.local/share/opencode"
`
	doc, err := parseTOML(src)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if doc["base_url"] != "https://openrouter.ai/api/v1" {
		t.Errorf("base_url = %v", doc["base_url"])
	}
	if doc["model"] != "some/model" {
		t.Errorf("model = %v", doc["model"])
	}
	if doc["max_input_tokens"] != int64(100000) {
		t.Errorf("max_input_tokens = %T %v", doc["max_input_tokens"], doc["max_input_tokens"])
	}
	if doc["max_output_tokens"] != int64(8000) {
		t.Errorf("max_output_tokens = %v", doc["max_output_tokens"])
	}
	if doc["system_prompt_file"] != "" {
		t.Errorf("system_prompt_file = %v", doc["system_prompt_file"])
	}
	pats, ok := doc["redact_patterns"].([]any)
	if !ok || len(pats) != 2 || pats[0] != "(?i)internal-token-[a-z0-9]{20}" {
		t.Errorf("redact_patterns = %#v", doc["redact_patterns"])
	}
	extra, ok := doc["extra_body"].(map[string]any)
	if !ok {
		t.Fatalf("extra_body = %#v", doc["extra_body"])
	}
	if extra["temperature"] != float64(0.2) || extra["top_p"] != float64(0.9) ||
		extra["flag"] != true || extra["note"] != "x=y" {
		t.Errorf("extra_body = %#v", extra)
	}
	dirs, ok := doc["dirs"].(map[string]any)
	if !ok || dirs["claude"] != "~/.claude" || dirs["opencode"] != "~/.local/share/opencode" {
		t.Errorf("dirs = %#v", doc["dirs"])
	}
}

func TestParseTOMLEscapes(t *testing.T) {
	doc, err := parseTOML(`a = "tab\there\nand \"quoted\" and \\ done"
b = "unié"
c = 'no \escapes \d'
`)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if doc["a"] != "tab\there\nand \"quoted\" and \\ done" {
		t.Errorf("a = %q", doc["a"])
	}
	if doc["b"] != "unié" {
		t.Errorf("b = %q", doc["b"])
	}
	if doc["c"] != `no \escapes \d` {
		t.Errorf("c = %q", doc["c"])
	}
}

func TestParseTOMLErrors(t *testing.T) {
	tests := []string{
		"key\n",
		"key = \n",
		"key = \"unterminated\n",
		"key = [\"a\", \"b\"\n",
		"key = { a = 1\n",
		"key = whatisthis\n",
		"[unclosed\n",
		"a.b.c = 1\n",
	}
	for _, src := range tests {
		if _, err := parseTOML(src); err == nil {
			t.Errorf("parseTOML(%q) = nil error, want error", src)
		}
	}
}
