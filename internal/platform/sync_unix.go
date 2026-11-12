//go:build !windows

package platform

import (
	"fmt"
	"os"
)

// SyncDir flushes a directory's entries to stable storage. Renaming a file
// into place is only atomic, not durable, until the containing directory is
// fsynced: a crash after rename can otherwise leave the old name, or no name
// at all.
func SyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory %q for sync: %w", path, err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync directory %q: %w", path, err)
	}
	return nil
}
