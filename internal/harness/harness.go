package harness

import (
	"context"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// Adapter normalizes one harness's local storage into session.Session values.
type Adapter interface {
	Name() session.Harness
	// List returns sessions sorted by UpdatedAt desc, already filtered by
	// opt.Scope and, unless opt.IncludeArchived, without archived ones.
	List(ctx context.Context, opt ListOptions) ([]session.Meta, error)
	Load(ctx context.Context, m session.Meta) (session.Session, error)
}

type ListOptions struct {
	Scope           Scope
	IncludeArchived bool
	Limit           int
}

type Scope struct {
	All       bool
	Root      string
	OriginURL string
}
