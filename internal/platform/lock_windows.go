//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// windowsLockHandle keeps the locked file and the overlapped structure that
// identifies the locked byte range.
type windowsLockHandle struct {
	f  *os.File
	ol windows.Overlapped
}

// lockRangeOffset is the byte the advisory lock covers. It is deliberately
// far above the JSON holder metadata at offset 0 so diagnostics and
// ReadLockHolder can still read the owner while the lock is held. Locking
// beyond the current end of file is valid on Windows.
const lockRangeOffset = 4096

// AcquireLock takes a non-blocking exclusive lock on path.
func (windowsPlatform) AcquireLock(path string) (LockHandle, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", path, err)
	}
	h := &windowsLockHandle{f: f}
	h.ol.Offset = lockRangeOffset
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &h.ol)
	if err != nil {
		holder, _ := ReadLockHolder(path)
		f.Close()
		if isLockViolation(err) {
			return nil, &LockedError{Path: path, Holder: holder, Cause: err}
		}
		return nil, fmt.Errorf("lock %q: %w", path, err)
	}
	if err := writeLockHolder(f); err != nil {
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &h.ol)
		f.Close()
		return nil, fmt.Errorf("write lock metadata %q: %w", path, err)
	}
	return h, nil
}

// Release unlocks and closes the file. The file itself is not removed.
func (h *windowsLockHandle) Release() error {
	if h == nil || h.f == nil {
		return nil
	}
	f := h.f
	h.f = nil
	unlockErr := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &h.ol)
	return errors.Join(unlockErr, f.Close())
}

func isLockViolation(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
