package redact

import "regexp"

type ruleDef struct {
	pattern string
	kind    string
	repl    string
}

func marker(kind string) string {
	return "[REDACTED:" + kind + "]"
}

func compileRules(defs []ruleDef) []rule {
	rules := make([]rule, len(defs))
	for i, d := range defs {
		repl := d.repl
		if repl == "" {
			repl = marker(d.kind)
		}
		rules[i] = rule{re: regexp.MustCompile(d.pattern), repl: repl, kind: d.kind}
	}
	return rules
}

// builtin runs in this order; the assignment rule must come after the
// bearer and URL-credential forms so it does not eat their output.
var builtin = compileRules([]ruleDef{
	{pattern: `(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`, kind: "pem_key"},
	{pattern: `\b(AKIA|ASIA)[0-9A-Z]{16}\b`, kind: "aws_key"},
	{pattern: `\bgh[pousr]_[A-Za-z0-9]{36,}\b`, kind: "github_token"},
	{pattern: `\bgithub_pat_[A-Za-z0-9_]{22,}\b`, kind: "github_token"},
	{pattern: `\bsk-[A-Za-z0-9_-]{20,}`, kind: "openai_key"},
	{pattern: `\bxox[abprs]-[A-Za-z0-9-]{10,}`, kind: "slack_token"},
	{pattern: `\bAIza[0-9A-Za-z_-]{35}\b`, kind: "google_api_key"},
	{pattern: `\b(sk|rk)_(live|test)_[0-9A-Za-z]{16,}\b`, kind: "stripe_key"},
	{pattern: `\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`, kind: "jwt"},
	{pattern: `https://(discord(app)?\.com/api/webhooks|hooks\.slack\.com/services)/\S+`, kind: "webhook_url"},
	{pattern: `([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`, kind: "url_credential", repl: `$1[REDACTED]@`},
	{pattern: `(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{16,}`, kind: "bearer", repl: `$1 [REDACTED]`},
	{pattern: `(?i)\b([A-Z0-9_]*(SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_KEY|ACCESS_KEY|CREDENTIALS?)[A-Z0-9_]*)["']?(\s*[:=]\s*)["']?[^\s"',]{6,}["']?`, kind: "assignment", repl: `$1$3[REDACTED]`},
	{pattern: `\bglpat-[A-Za-z0-9_-]{20,}`, kind: "gitlab_token"},
	{pattern: `\bnpm_[A-Za-z0-9]{36}\b`, kind: "npm_token"},
	{pattern: `\bxapp-[A-Za-z0-9-]{10,}`, kind: "slack_app_token"},
	{pattern: `\b\d{8,10}:[A-Za-z0-9_-]{35}\b`, kind: "telegram_bot_token"},
})
