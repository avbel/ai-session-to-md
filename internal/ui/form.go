package ui

import (
	"context"
	"errors"
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// cancelKeys makes Esc cancel a form as well as Ctrl-C; both map to
// exit 130 (§4). Esc never reaches the fields, so it cannot double as a
// filter toggle. "q" stays free on purpose: with filtering on it types into
// the filter.
func cancelKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return km
}

// formTheme styles huh fields: the Charm theme on a color TTY, a colorless
// variant under --no-color / NO_COLOR.
func (u *UI) formTheme() huh.Theme {
	if u.Color {
		return huh.ThemeFunc(huh.ThemeCharm)
	}
	return huh.ThemeFunc(plainTheme)
}

// plainTheme strips every color from huh's base theme, so a NoColor run
// emits no escape sequences.
func plainTheme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	plain := lipgloss.NewStyle()
	button := lipgloss.NewStyle().Padding(0, 2).MarginRight(1)
	t.Focused.FocusedButton = button
	t.Focused.BlurredButton = plain
	t.Focused.TextInput.Placeholder = plain
	t.Blurred.FocusedButton = button
	t.Blurred.BlurredButton = plain
	t.Blurred.TextInput.Placeholder = plain
	return t
}

// runForm renders one huh group on stderr and waits for the user. Cancellation
// (Esc, Ctrl-C, or the context) is reported as ErrCancelled.
func (u *UI) runForm(ctx context.Context, group *huh.Group) error {
	form := huh.NewForm(group).
		WithShowHelp(false).
		WithOutput(u.Stderr).
		WithTheme(u.formTheme()).
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
