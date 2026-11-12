package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOCK")
	first, err := Current().AcquireLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	holder, err := ReadLockHolder(path)
	if err != nil {
		t.Fatalf("read holder: %v", err)
	}
	if holder.PID != int32(os.Getpid()) {
		t.Fatalf("holder pid = %d, want %d", holder.PID, os.Getpid())
	}
	if holder.Hostname == "" {
		t.Fatal("holder hostname is empty")
	}

	second, err := Current().AcquireLock(path)
	if err == nil {
		_ = second.Release()
		t.Fatal("second acquire succeeded while the lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire error = %v, want ErrLocked", err)
	}
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("second acquire error = %T, want *LockedError", err)
	}
	if locked.Holder.PID != holder.PID {
		t.Fatalf("locked holder pid = %d, want %d", locked.Holder.PID, holder.PID)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("lock file was removed on release: %v", err)
	}

	third, err := Current().AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release third: %v", err)
	}
}

func TestReadLockHolderMissingFileReturnsOSError(t *testing.T) {
	_, err := ReadLockHolder(filepath.Join(t.TempDir(), "LOCK"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadLockHolder error = %v, want os.ErrNotExist", err)
	}
}
