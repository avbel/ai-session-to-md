package codex

import "testing"

// makeFifo skips on Windows: mkfifo has no syscall equivalent.
func makeFifo(t *testing.T, dir, name string) string {
	t.Helper()
	t.Skip("mkfifo is unavailable on windows")
	return ""
}
