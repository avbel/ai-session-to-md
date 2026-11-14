package ui

import (
	"context"
	"errors"
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
)

// cancelKeys makes Esc cancel a form as well as Ctrl-C; both map to
// exit 130 (§4). Esc never reaches the fields, so it cannot double as a
// filter toggle.
func cancelKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return km
}

// formTheme styles huh fields; plain Charm colors on a TTY.
func formTheme() huh.Theme {
	return huh.ThemeFunc(huh.ThemeCharm)
}

// runForm renders one huh group on stderr and waits for the user. Cancellation
// (Esc, Ctrl-C, or the context) is reported as ErrCancelled.
func (u *UI) runForm(ctx context.Context, group *huh.Group) error {
	form := huh.NewForm(group).
		WithShowHelp(false).
		WithOutput(u.Stderr).
		WithTheme(formTheme()).
		WithKeyMap(cancelKeyMap())
	if u.Stdin != nil {
		form = form.WithInput(u.Stdin)
	}
	err := form.RunWithContext(ctx)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, huh.ErrUserAborted):
		return ErrCancelled
	case ctx.Err() != nil:
		return fmt.Errorf("form: %w: %v", ErrCancelled, ctx.Err())
	default:
		return fmt.Errorf("form: %w", err)
	}
}
