package redact

import (
	"strings"
	"testing"
)

func newTestRedactor(t *testing.T, patterns ...string) *Redactor {
	t.Helper()
	r, err := New(WithPatterns(patterns))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestBuiltinPatterns(t *testing.T) {
	cases := []struct {
		name string
		kind string
		pos  string
		want string
		neg  string
	}{
		{
			name: "pem_key",
			kind: "pem_key",
			pos: "-----BEGIN " + "OPENSSH PRIVATE KEY" + "-----\n" +
				strings.Repeat("b3Bh", 40) + "\n-----END " + "OPENSSH PRIVATE KEY" + "-----",
			want: "[REDACTED:pem_key]",
			neg:  "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----",
		},
		{
			name: "aws_key",
			kind: "aws_key",
			pos:  "key " + "AKIA" + "ABCDEFGHIJKLMNOP" + " end",
			want: "[REDACTED:aws_key]",
			neg:  "key " + "AKIA" + "ABCDEFGHIJKLMNO" + " end",
		},
		{
			name: "github_token",
			kind: "github_token",
			pos:  "token " + "ghp_" + strings.Repeat("A1", 18),
			want: "[REDACTED:github_token]",
			neg:  "token " + "ghp_" + strings.Repeat("A1", 17) + "A",
		},
		{
			name: "github_fine_grained",
			kind: "github_token",
			pos:  "token " + "github_pat_" + "Ab1_" + strings.Repeat("x", 18),
			want: "[REDACTED:github_token]",
			neg:  "token " + "github_pat_" + "Ab1_" + strings.Repeat("x", 17),
		},
		{
			name: "openai_key",
			kind: "openai_key",
			pos:  "key " + "sk-" + strings.Repeat("aB9_-", 4),
			want: "[REDACTED:openai_key]",
			neg:  "key " + "sk-" + strings.Repeat("a", 19),
		},
		{
			name: "slack_token",
			kind: "slack_token",
			pos:  "token " + "xoxb-" + strings.Repeat("a1-", 4),
			want: "[REDACTED:slack_token]",
			neg:  "token " + "xoxb-" + strings.Repeat("a", 9),
		},
		{
			name: "google_api_key",
			kind: "google_api_key",
			pos:  "key " + "AIza" + strings.Repeat("0Az-_", 7),
			want: "[REDACTED:google_api_key]",
			neg:  "key " + "AIza" + strings.Repeat("0", 34),
		},
		{
			name: "stripe_key",
			kind: "stripe_key",
			pos:  "key " + "sk_" + "live_" + strings.Repeat("9f", 8),
			want: "[REDACTED:stripe_key]",
			neg:  "key " + "sk_" + "prod_" + strings.Repeat("9f", 8),
		},
		{
			name: "jwt",
			kind: "jwt",
			pos:  "eyJ" + strings.Repeat("e30", 4) + "." + strings.Repeat("pay", 4) + "." + strings.Repeat("sig", 4),
			want: "[REDACTED:jwt]",
			neg:  "eyJ" + strings.Repeat("e30", 4) + "." + strings.Repeat("pay", 4),
		},
		{
			name: "webhook_url",
			kind: "webhook_url",
			pos: "see https://hooks." + "slack.com/services/" + "T00/B00/xxxxx and https://discord" +
				".com/api/webhooks/" + "1234567890/" + strings.Repeat("Ab", 10),
			want: "[REDACTED:webhook_url]",
			neg:  "see https://discord.com/api/channels/123 and https://example.com/services/x",
		},
		{
			name: "url_credential",
			kind: "url_credential",
			pos:  "postgres://" + "user" + ":" + "secret" + "@db.example.com:5432/app",
			want: "postgres://[REDACTED]@db.example.com:5432/app",
			neg:  "postgres://db.example.com:5432/app",
		},
		{
			name: "bearer",
			kind: "bearer",
			pos:  "Authorization: Bearer " + strings.Repeat("t0k", 6),
			want: "Authorization: Bearer [REDACTED]",
			neg:  "Authorization: Bearer " + strings.Repeat("a", 15),
		},
		{
			name: "assignment",
			kind: "assignment",
			pos:  `MY_SECRET_TOKEN = "abc123def456"`,
			want: "MY_SECRET_TOKEN = [REDACTED]",
			neg:  "MY_TOKEN=abc",
		},
		{
			name: "assignment_json",
			kind: "assignment",
			pos:  `"api_key": "value1234567"`,
			want: `"api_key: [REDACTED]`,
			neg:  `"api_key": "abc"`,
		},
		{
			name: "gitlab_token",
			kind: "gitlab_token",
			pos:  "token " + "glpat-" + strings.Repeat("K3", 10),
			want: "[REDACTED:gitlab_token]",
			neg:  "token " + "glpat-" + strings.Repeat("K", 19),
		},
		{
			name: "npm_token",
			kind: "npm_token",
			pos:  "token " + "npm_" + strings.Repeat("d7", 18),
			want: "[REDACTED:npm_token]",
			neg:  "token " + "npm_" + strings.Repeat("d7", 17) + "d",
		},
		{
			name: "slack_app_token",
			kind: "slack_app_token",
			pos:  "token " + "xapp-" + strings.Repeat("q1-", 4),
			want: "[REDACTED:slack_app_token]",
			neg:  "token " + "xapp-" + strings.Repeat("q", 9),
		},
		{
			name: "telegram_bot_token",
			kind: "telegram_bot_token",
			pos:  "token 12345678:" + strings.Repeat("zA4_", 8) + "zzz",
			want: "[REDACTED:telegram_bot_token]",
			neg:  "token 1234567:" + strings.Repeat("z", 35),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRedactor(t)
			got := r.Apply(tc.pos)
			if !strings.Contains(got.Text, tc.want) {
				t.Errorf("positive: got %q, want it to contain %q", got.Text, tc.want)
			}
			if got.Counts[tc.kind] < 1 {
				t.Errorf("positive: counts[%s] = %d, want >= 1", tc.kind, got.Counts[tc.kind])
			}
			clean := r.Apply(tc.neg)
			if clean.Text != tc.neg {
				t.Errorf("negative: got %q, want unchanged %q", clean.Text, tc.neg)
			}
			if clean.Total() != 0 {
				t.Errorf("negative: counts = %v, want no matches", clean.Counts)
			}
		})
	}
}

func TestUserPatterns(t *testing.T) {
	r := newTestRedactor(t, `internal-token-[a-z0-9]{20}`)
	in := "got internal-token-" + "abcdefghijklmnopqrst" + " back"
	got := r.Apply(in)
	if want := "got [REDACTED:user] back"; got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["user"] != 1 {
		t.Errorf("counts[user] = %d, want 1", got.Counts["user"])
	}
}

func TestUserPatternOptionsAppend(t *testing.T) {
	r, err := New(WithPatterns([]string{`zzz-secret-[a-z]{3}`}), WithPatterns([]string{`qqq-token-[a-z]{3}`}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := r.Apply("zzz-secret-abc qqq-token-def")
	if got.Counts["user"] != 2 {
		t.Errorf("counts[user] = %d, want 2", got.Counts["user"])
	}
}

func TestInvalidUserPattern(t *testing.T) {
	_, err := New(WithPatterns([]string{"[unclosed"}))
	if err == nil {
		t.Fatal("want an error for an invalid pattern")
	}
	if !strings.Contains(err.Error(), "redact pattern") {
		t.Errorf("error = %v, want context about the pattern", err)
	}
}

func TestHomeDirReplaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r := newTestRedactor(t)
	got := r.Apply(home + "/projects/x and " + home + "/logs")
	if want := "~/projects/x and ~/logs"; got.Text != want {
		t.Errorf("got %q, want %q", got.Text, want)
	}
	if got.Counts["home"] != 2 {
		t.Errorf("counts[home] = %d, want 2", got.Counts["home"])
	}
}

func TestApplyCountsAndTotal(t *testing.T) {
	r := newTestRedactor(t)
	in := "AKIA" + "ABCDEFGHIJKLMNOP" + " and " + "sk-" + strings.Repeat("a", 20) +
		" and " + "AKIA" + "QRSTUVWXYZ012345"
	got := r.Apply(in)
	if got.Counts["aws_key"] != 2 {
		t.Errorf("counts[aws_key] = %d, want 2", got.Counts["aws_key"])
	}
	if got.Counts["openai_key"] != 1 {
		t.Errorf("counts[openai_key] = %d, want 1", got.Counts["openai_key"])
	}
	if got.Total() != 3 {
		t.Errorf("Total = %d, want 3", got.Total())
	}
}

func TestApplyEmpty(t *testing.T) {
	r := newTestRedactor(t)
	got := r.Apply("")
	if got.Text != "" {
		t.Errorf("Text = %q, want empty", got.Text)
	}
	if got.Total() != 0 {
		t.Errorf("Total = %d, want 0", got.Total())
	}
}
