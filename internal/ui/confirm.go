package ui

import (
	"context"

	"charm.land/huh/v2"
)

// Confirm asks a yes/no question on the TTY. The default is No; Esc or
// Ctrl-C returns ErrCancelled. The caller must ensure a TTY exists
// (OverwriteDecision) before calling.
func (u *UI) Confirm(ctx context.Context, prompt, description string) (bool, error) {
	var yes bool
	c := huh.NewConfirm().
		Title(prompt).
		Description(description).
		Affirmative("Yes").
		Negative("No").
		Value(&yes)
	if err := u.runForm(ctx, huh.NewGroup(c)); err != nil {
		return false, err
	}
	return yes, nil
}
