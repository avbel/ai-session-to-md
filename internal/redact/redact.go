package redact

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Result carries the redacted text and how many matches each kind had.
type Result struct {
	Text   string
	Counts map[string]int
}

// Total is the number of redactions across all kinds.
func (r Result) Total() int {
	total := 0
	for _, n := range r.Counts {
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

// Apply replaces the home dir with ~, then runs every pattern over the full
// text. Call it on untruncated values; a cut secret no longer matches.
func (r *Redactor) Apply(text string) Result {
	counts := make(map[string]int)
	if text == "" {
		return Result{Text: "", Counts: counts}
	}
	if r.home != "" {
		if n := strings.Count(text, r.home); n > 0 {
			counts["home"] = n
			text = strings.ReplaceAll(text, r.home, "~")
		}
	}
	for _, ru := range r.rules {
		text = ru.apply(text, counts)
	}
	return Result{Text: text, Counts: counts}
}
