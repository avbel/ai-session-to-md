package ui

import (
	"bufio"
	"context"
	"fmt"
	"strings"
)

// Confirm asks a yes/no question on the TTY and reads the answer from
// stdin. Anything other than y/Y answers No; the default is No. The caller
// must ensure a TTY exists (OverwriteDecision) before calling.
func (u *UI) Confirm(ctx context.Context, prompt string) (bool, error) {
	_, _ = fmt.Fprint(u.Stderr, prompt+" [y/N] ")
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(u.Stdin).ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("confirm: %w: %v", ErrCancelled, ctx.Err())
	case r := <-ch:
		if r.err != nil && r.line == "" {
			return false, fmt.Errorf("confirm: %w: %v", ErrCancelled, r.err)
		}
		answer := strings.TrimRight(r.line, "\r\n")
		answer = strings.TrimSpace(answer)
		return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
	}
}
