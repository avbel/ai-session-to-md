package opencode

import (
	"github.com/avbel/ai-session-to-md/internal/harness"
)

// harnessListOpt is a small builder for harness.ListOptions in tests.
type harnessListOpt struct {
	scope    harness.Scope
	archived bool
	limit    int
}

func (o harnessListOpt) opt() harness.ListOptions {
	return harness.ListOptions{Scope: o.scope, IncludeArchived: o.archived, Limit: o.limit}
}

func withScopeRoot(root string) func(*harnessListOpt) {
	return func(o *harnessListOpt) { o.scope.Root = root }
}

func withScopeAll() func(*harnessListOpt) {
	return func(o *harnessListOpt) { o.scope.All = true }
}

func withArchived() func(*harnessListOpt) {
	return func(o *harnessListOpt) { o.archived = true }
}

func withLimit(n int) func(*harnessListOpt) {
	return func(o *harnessListOpt) { o.limit = n }
}

func listOptAll() harness.ListOptions {
	return harness.ListOptions{Scope: harness.Scope{All: true}}
}

func listOpts(opts ...func(*harnessListOpt)) harness.ListOptions {
	o := harnessListOpt{}
	for _, f := range opts {
		f(&o)
	}
	return o.opt()
}
