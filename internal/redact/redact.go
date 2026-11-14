package redact

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Result carries the redacted text and how many matches each kind had.
// Counts includes the "home" replacements; Total() does not.
type Result struct {
	Text   string
	Counts map[string]int
}

// Total is the number of redacted secrets across all kinds. Home-dir
// replacements are not secrets, so a session whose only redaction was
// path folding reports 0.
func (r Result) Total() int {
	total := 0
	for kind, n := range r.Counts {
		if kind == homeKind {
			continue
		}
		total += n
	}
	return total
}

// Option configures a Redactor at construction.
type Option func(*options) error

type options struct {
	patterns []string
}

// WithPatterns adds extra regexes from the user config. They are compiled
// and validated when New applies the option; kind "user".
func WithPatterns(patterns []string) Option {
	return func(o *options) error {
		o.patterns = append(o.patterns, patterns...)
		return nil
	}
}

type rule struct {
	re   *regexp.Regexp
	repl string
	kind string
}

func (r rule) apply(text string, counts map[string]int) string {
	locs := r.re.FindAllStringIndex(text, -1)
	if locs == nil {
		return text
	}
	counts[r.kind] += len(locs)
	return r.re.ReplaceAllString(text, r.repl)
}

// Redactor holds the precompiled patterns and the user home dir.
type Redactor struct {
	rules []rule
	home  string
}

// New builds a Redactor with the built-in patterns in fixed order, followed
// by any user patterns. An invalid user pattern is an error here, even if it
// also failed config validation.
func New(opts ...Option) (*Redactor, error) {
	var o options
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return nil, fmt.Errorf("build redactor: %w", err)
		}
	}
	rules := make([]rule, 0, len(builtin)+len(o.patterns))
	rules = append(rules, builtin...)
	for _, p := range o.patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		rules = append(rules, rule{re: re, repl: marker("user"), kind: "user"})
	}
	home, _ := os.UserHomeDir()
	return &Redactor{rules: rules, home: home}, nil
}

// Apply runs every pattern over the full, untruncated text, then replaces
// the home dir with ~. Patterns must run first: a naive early home
// replacement would corrupt what they match (with HOME=/ every "/" would
// become "~" before any URL rule could see it). A cut secret no longer
// matches, so call it on untruncated values.
func (r *Redactor) Apply(text string) Result {
	counts := make(map[string]int)
	if text == "" {
		return Result{Text: "", Counts: counts}
	}
	for _, ru := range r.rules {
		text = ru.apply(text, counts)
	}
	if len(r.home) > 1 {
		var n int
		text, n = replaceHome(text, r.home)
		if n > 0 {
			counts[homeKind] = n
		}
	}
	return Result{Text: text, Counts: counts}
}

// homeKind counts home-dir replacements in Counts, but is excluded from
// Total() because a folded path is not a secret.
const homeKind = "home"

// replaceHome replaces occurrences of home with ~ only at path boundaries:
// the character after the match is "/", a non-path character, or the end
// of the text. With home /Users/al, /Users/alice keeps its name.
func replaceHome(text, home string) (string, int) {
	var b strings.Builder
	b.Grow(len(text))
	n := 0
	for {
		i := strings.Index(text, home)
		if i < 0 {
			break
		}
		end := i + len(home)
		b.WriteString(text[:i])
		if end < len(text) && isPathChar(text[end]) {
			b.WriteString(home)
		} else {
			b.WriteByte('~')
			n++
		}
		text = text[end:]
	}
	b.WriteString(text)
	return b.String(), n
}

// isPathChar reports whether c continues a path name right after the home
// prefix, so replacing before it would corrupt a longer name ("/Users/al"
// in "/Users/alice" or "/Users/al.txt"). "/" is NOT a path char: it starts
// the path under home ("~/x").
func isPathChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '-' || c == '.':
		return true
	default:
		return false
	}
}
