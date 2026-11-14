//go:build !windows

package codex

import (
	"os"
	"syscall"
	"testing"
)

// makeFifo creates a FIFO for the non-regular-file tests; POSIX only.
func makeFifo(t *testing.T, dir, name string) string {
	t.Helper()
	fifo := dir + string(os.PathSeparator) + name
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo not available: %v", err)
	}
	return fifo
}
