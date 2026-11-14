package handoff

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes content atomically: a temp file in the target's
// directory, fsync, rename, mode 0644. A symlinked target is refused.
func WriteFile(path string, content []byte) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s: target is a symlink", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	if err := commitTemp(tmp, content, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// commitTemp writes, syncs, closes and renames the temp file, removing it on
// any failure.
func commitTemp(tmp *os.File, content []byte, path string) error {
	name := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
