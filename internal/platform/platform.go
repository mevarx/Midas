// Package platform isolates every OS-specific behaviour behind one interface.
//
// No package outside platform may branch on runtime.GOOS. If something new is
// needed on one platform, extend this interface and implement it in the
// build-tagged files.
package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ProcInfo describes one running process that matched a detection rule.
type ProcInfo struct {
	PID     int32
	Name    string
	Cmdline string
}

// Platform is the OS abstraction used by the rest of Midas.
type Platform interface {
	// CreateLink creates a link at link pointing to the directory target.
	// On Windows this is a directory junction; elsewhere a symlink.
	CreateLink(link, target string) error

	// ReadLink reports whether path is a Midas-style link and, if so, where
	// it points. It never follows the link.
	ReadLink(path string) (target string, isLink bool, err error)

	// ResolvePath resolves every link component of path (including Windows
	// junctions, which filepath.EvalSymlinks ignores) and returns the
	// absolute resolved path. A missing leaf is tolerated: resolution
	// stops at the first component that does not exist and joins the
	// remainder verbatim.
	ResolvePath(path string) (string, error)

	// RemoveLink removes a link without touching its target's contents.
	RemoveLink(path string) error

	// FreeSpace returns free bytes on the volume containing path.
	FreeSpace(path string) (uint64, error)

	// VolumeID returns an identifier for the volume containing path. Two
	// paths with the same VolumeID can be renamed rather than copied.
	VolumeID(path string) (string, error)

	// FindProcesses returns running processes matching names (compared
	// case-insensitively against executable basenames) and, when
	// cmdlineContains is non-empty, whose command line contains at least one
	// of the substrings.
	FindProcesses(names []string, cmdlineContains []string) ([]ProcInfo, error)

	// TempEnvVars lists the environment variable names that control temp
	// directories on this OS, in priority order.
	TempEnvVars() []string

	// ConfigDir returns the OS-appropriate directory for midas config.
	ConfigDir() string

	// PathTokens returns expansion values for {home}, {xdg_*}, {appdata}, ...
	PathTokens() map[string]string

	// IsProcessAlive reports whether a PID belongs to a running process.
	IsProcessAlive(pid int32) bool

	// AcquireLock takes an exclusive advisory lock on path, creating the file
	// if needed, and writes the holder metadata into it. It never blocks: a
	// lock held by anyone else returns a *LockedError.
	AcquireLock(path string) (LockHandle, error)
}

// LockHolder identifies the process that owns a vault lock.
type LockHolder struct {
	PID       int32     `json:"pid"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}

// LockHandle is an acquired advisory lock. Release unlocks and closes the
// underlying file; the lock file itself is kept so its metadata stays
// readable.
type LockHandle interface {
	Release() error
}

// ErrLocked reports that a lock is held by another process.
var ErrLocked = errors.New("lock is held by another process")

// LockedError describes a lock that could not be acquired.
type LockedError struct {
	Path   string
	Holder LockHolder
	Cause  error
}

func (e *LockedError) Error() string {
	who := "another process"
	if e.Holder.PID != 0 {
		details := fmt.Sprintf("pid %d", e.Holder.PID)
		if e.Holder.Hostname != "" {
			details += ", host " + e.Holder.Hostname
		}
		if !e.Holder.StartedAt.IsZero() {
			details += ", started " + e.Holder.StartedAt.UTC().Format(time.RFC3339)
		}
		who += " (" + details + ")"
	}
	if e.Path == "" {
		if e.Holder.PID == 0 {
			return ErrLocked.Error()
		}
		return fmt.Sprintf("%s: %s", ErrLocked.Error(), who)
	}
	return fmt.Sprintf("lock %q is held by %s", e.Path, who)
}

func (e *LockedError) Unwrap() error {
	if e.Cause != nil {
		return errors.Join(ErrLocked, e.Cause)
	}
	return ErrLocked
}

// ReadLockHolder reads and parses the metadata written when a lock was
// acquired. A missing file returns the os error unchanged.
func ReadLockHolder(path string) (LockHolder, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LockHolder{}, err
	}
	var holder LockHolder
	if err := json.Unmarshal(data, &holder); err != nil {
		return LockHolder{}, err
	}
	return holder, nil
}

// writeLockHolder overwrites the locked file with this process's metadata.
func writeLockHolder(f *os.File) error {
	hostname, _ := os.Hostname()
	data, err := json.Marshal(LockHolder{
		PID:       int32(os.Getpid()),
		Hostname:  hostname,
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(data, 0); err != nil {
		return err
	}
	return f.Sync()
}

// Current returns the Platform implementation for the running OS.
func Current() Platform { return current }

var current = newPlatform()

// IsRealDirectory reports whether path is a real directory: it exists, is a
// directory, and is not a link (symlink on Unix, symlink or directory
// junction on Windows). Junctions are not flagged by os.ModeSymlink, so
// callers must use this instead of hand-rolled Lstat checks.
func IsRealDirectory(path string) bool {
	_, isLink, err := current.ReadLink(path)
	if err != nil || isLink {
		return false
	}
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0
}

// IsDirectoryLink reports whether a known link points at a directory. The
// link's own Lstat entry is useless for this on Windows (a junction reports
// as an irregular file), so the target is statted instead. A dangling or
// unreadable target reports false.
func IsDirectoryLink(linkPath string) bool {
	info, err := os.Stat(linkPath)
	return err == nil && info.IsDir()
}

// LinkType returns the human-readable link type for the current OS.
func LinkType() string {
	if runtime.GOOS == "windows" {
		return "junction"
	}
	return "symlink"
}

// ErrNotLink is returned by RemoveLink when the path is not a link.
var ErrNotLink = errors.New("path is not a link")

// HomeDir returns the current user's home directory.
func HomeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return h, nil
}

// OS returns the current operating system name.
func OS() string { return runtime.GOOS }

// SamePath compares two cleaned paths using the OS's case rules.
func SamePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// IsVolumeRoot reports whether p is a filesystem root.
func IsVolumeRoot(p string) bool {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(clean)
		return vol != "" && strings.TrimSuffix(clean, string(filepath.Separator)) == strings.TrimSuffix(vol, string(filepath.Separator))
	}
	return clean == "/"
}

// EnvKeysEqual compares environment variable names using the OS's rules.
func EnvKeysEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// SystemDirs lists OS directories Midas must never treat as a target.
func SystemDirs() []string {
	switch runtime.GOOS {
	case "windows":
		var out []string
		for _, env := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
			if v := os.Getenv(env); v != "" {
				out = append(out, v)
			}
		}
		return append(out, `C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\ProgramData`)
	case "darwin":
		return []string{"/System", "/Library", "/usr", "/bin", "/sbin", "/etc", "/private", "/Applications"}
	default:
		return []string{"/usr", "/etc", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/var", "/opt", "/srv"}
	}
}
