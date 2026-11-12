//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// unixLockHandle keeps the open file description that owns the flock so it
// stays locked until Release.
type unixLockHandle struct {
	f *os.File
}

// AcquireLock takes a non-blocking flock on path.
func (unixPlatform) AcquireLock(path string) (LockHandle, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := ReadLockHolder(path)
		f.Close()
		return nil, &LockedError{Path: path, Holder: holder, Cause: err}
	}
	if err := writeLockHolder(f); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("write lock metadata %q: %w", path, err)
	}
	return &unixLockHandle{f: f}, nil
}

// Release unlocks and closes the file. The file itself is not removed.
func (h *unixLockHandle) Release() error {
	if h == nil || h.f == nil {
		return nil
	}
	f := h.f
	h.f = nil
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return errors.Join(unlockErr, f.Close())
}
