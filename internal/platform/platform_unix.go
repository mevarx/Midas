//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v4/process"
)

type unixPlatform struct{}

func newPlatform() Platform { return unixPlatform{} }

func (unixPlatform) CreateLink(link, target string) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve link target %q: %w", target, err)
	}
	if _, err := os.Lstat(link); err == nil {
		return fmt.Errorf("cannot create link: %q already exists", link)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("create parent of %q: %w", link, err)
	}
	if err := os.Symlink(absTarget, link); err != nil {
		return fmt.Errorf("create symlink %q -> %q: %w", link, absTarget, err)
	}
	return nil
}

func (unixPlatform) ReadLink(path string) (string, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return "", false, nil
	}
	t, err := os.Readlink(path)
	if err != nil {
		return "", false, err
	}
	return t, true, nil
}

func (unixPlatform) RemoveLink(path string) error {
	_, isLink, err := unixCurrent().ReadLink(path)
	if err != nil {
		return err
	}
	if !isLink {
		return ErrNotLink
	}
	return os.Remove(path)
}

func unixCurrent() Platform { return unixPlatform{} }

func (unixPlatform) VolumeID(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		fi, err := os.Stat(abs)
		if err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				return fmt.Sprintf("dev:%d", uint64(st.Dev)), nil
			}
			return "", errors.New("cannot determine device for " + abs)
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", err
		}
		abs = parent
	}
}

func (unixPlatform) FindProcesses(names, cmdlineContains []string) ([]ProcInfo, error) {
	out, err := exec.Command("ps", "-eo", "pid=,comm=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes with ps: %w", err)
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.ToLower(filepath.Base(n))] = true
	}
	var res []ProcInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil {
			continue
		}
		comm := fields[1]
		args := ""
		if len(fields) > 2 {
			args = strings.Join(fields[2:], " ")
		}
		name := strings.ToLower(filepath.Base(comm))
		if !want[name] {
			// ps comm can be truncated; also check argv[0].
			argv0 := strings.Fields(args)
			if len(argv0) == 0 || !want[strings.ToLower(filepath.Base(argv0[0]))] {
				continue
			}
		}
		if len(cmdlineContains) > 0 && !containsAnyFold(args, cmdlineContains) {
			continue
		}
		res = append(res, ProcInfo{PID: int32(pid), Name: comm, Cmdline: args})
	}
	return res, nil
}

func containsAnyFold(s string, subs []string) bool {
	ls := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(ls, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func (unixPlatform) TempEnvVars() []string { return []string{"TMPDIR", "TMP", "TEMP"} }

func (unixPlatform) IsProcessAlive(pid int32) bool {
	ok, err := process.PidExists(pid)
	return err == nil && ok
}

func (unixPlatform) ConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "midas")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "midas")
}

func (unixPlatform) PathTokens() map[string]string {
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		if runtime.GOOS == "darwin" {
			cfg = filepath.Join(home, "Library", "Application Support")
		} else {
			cfg = filepath.Join(home, ".config")
		}
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		if runtime.GOOS == "darwin" {
			data = filepath.Join(home, "Library", "Application Support")
		} else {
			data = filepath.Join(home, ".local", "share")
		}
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		if runtime.GOOS == "darwin" {
			cache = filepath.Join(home, "Library", "Caches")
		} else {
			cache = filepath.Join(home, ".cache")
		}
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		if runtime.GOOS == "darwin" {
			state = filepath.Join(home, "Library", "Application Support")
		} else {
			state = filepath.Join(home, ".local", "state")
		}
	}
	return map[string]string{
		"home":         home,
		"appdata":      cfg,
		"localappdata": cfg,
		"xdg_config":   cfg,
		"xdg_data":     data,
		"xdg_cache":    cache,
		"xdg_state":    state,
	}
}
