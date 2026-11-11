//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
)

const (
	fsctlGetReparsePoint = 0x000900A8
	fsctlSetReparsePoint = 0x000900A4

	ioReparseTagMountPoint = 0xA0000003
	maxReparseDataSize     = 16 * 1024

	fileFlagBackupSemantics  = 0x02000000
	fileFlagOpenReparsePoint = 0x00200000
	genericWrite             = 0x40000000
	genericRead              = 0x80000000
	openExisting             = 3
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileW           = kernel32.NewProc("CreateFileW")
	procDeviceIoControl       = kernel32.NewProc("DeviceIoControl")
	procGetDiskFreeSpaceExW   = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetVolumePathNameW    = kernel32.NewProc("GetVolumePathNameW")
	procGetVolumeInformationW = kernel32.NewProc("GetVolumeInformationW")
	procRemoveDirectoryW      = kernel32.NewProc("RemoveDirectoryW")
)

type windowsPlatform struct{}

func newPlatform() Platform { return windowsPlatform{} }

// longPath adds the \\?\ prefix required for paths beyond MAX_PATH.
func longPath(p string) string {
	p = filepath.Clean(p)
	if strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(p, `\\`)
	}
	return `\\?\` + p
}

func u16ptr(s *uint16) uintptr { return uintptr(unsafe.Pointer(s)) }

// CreateLink creates a directory junction at link pointing to target.
// It uses the reparse-point API directly and falls back to `cmd /c mklink /J`.
func (windowsPlatform) CreateLink(link, target string) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve link target %q: %w", target, err)
	}
	absLink, err := filepath.Abs(link)
	if err != nil {
		return fmt.Errorf("resolve link path %q: %w", link, err)
	}
	if _, statErr := os.Lstat(absLink); statErr == nil {
		return fmt.Errorf("cannot create link: %q already exists", absLink)
	}

	// The junction is set on an existing, empty directory.
	if err := os.MkdirAll(absLink, 0o755); err != nil {
		return fmt.Errorf("create junction directory %q: %w", absLink, err)
	}
	if err := setMountPoint(absLink, absTarget); err != nil {
		_ = os.Remove(absLink)
		if fbErr := junctionViaCmd(absLink, absTarget); fbErr != nil {
			return fmt.Errorf("create junction %q -> %q: %w (fallback: %v)", absLink, absTarget, err, fbErr)
		}
	}
	return nil
}

func setMountPoint(link, target string) error {
	linkPtr, err := syscall.UTF16PtrFromString(longPath(link))
	if err != nil {
		return err
	}
	h, _, errno := procCreateFileW.Call(
		uintptr(unsafe.Pointer(linkPtr)),
		genericWrite,
		0, // no sharing
		0,
		openExisting,
		fileFlagBackupSemantics|fileFlagOpenReparsePoint,
		0,
	)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return fmt.Errorf("open junction directory: %w", errno)
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	sub := `\??\` + filepath.Clean(target)
	subU16 := utf16.Encode([]rune(sub))
	printU16 := utf16.Encode([]rune(filepath.Clean(target)))

	// REPARSE_DATA_BUFFER header (8 bytes) + MountPoint buffer (8 bytes) + path data.
	pathBytes := (len(subU16) + 1 + len(printU16) + 1) * 2
	buf := make([]byte, 8+8+pathBytes)
	binary.LittleEndian.PutUint32(buf[0:], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buf[4:], uint16(8+pathBytes))
	binary.LittleEndian.PutUint16(buf[6:], 0)
	binary.LittleEndian.PutUint16(buf[8:], 0)                          // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:], uint16(len(subU16)*2))     // SubstituteNameLength
	binary.LittleEndian.PutUint16(buf[12:], uint16((len(subU16)+1)*2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(printU16)*2))   // PrintNameLength

	off := 16
	for _, c := range subU16 {
		binary.LittleEndian.PutUint16(buf[off:], c)
		off += 2
	}
	off += 2 // null terminator
	for _, c := range printU16 {
		binary.LittleEndian.PutUint16(buf[off:], c)
		off += 2
	}

	var returned uint32
	r, _, errno := procDeviceIoControl.Call(
		h,
		fsctlSetReparsePoint,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0,
		uintptr(unsafe.Pointer(&returned)),
		0,
	)
	if r == 0 {
		return fmt.Errorf("FSCTL_SET_REPARSE_POINT: %w", errno)
	}
	return nil
}

func junctionViaCmd(link, target string) error {
	cmd := fmt.Sprintf(`mklink /J "%s" "%s"`, longPath(link), filepath.Clean(target))
	out, err := runCmd("cmd", "/c", cmd)
	if err != nil {
		return fmt.Errorf("mklink /J: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (windowsPlatform) ReadLink(path string) (string, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	// Plain symlink (file or directory).
	if fi.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		if err != nil {
			return "", false, err
		}
		return t, true, nil
	}
	// Mount-point reparse point (directory junction). Go 1.23+ reports
	// junctions as irregular files (`os.ModeIrregular`, not a directory),
	// so this must not be gated on IsDir. A reparse query on a regular
	// file or directory just answers "not a reparse point".
	if fi.Mode().IsRegular() || fi.IsDir() || fi.Mode()&os.ModeIrregular != 0 {
		t, ok, err := readMountPoint(path)
		if err != nil || !ok {
			return "", ok, err
		}
		return t, true, nil
	}
	return "", false, nil
}

func readMountPoint(path string) (string, bool, error) {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return "", false, err
	}
	h, _, errno := procCreateFileW.Call(
		uintptr(unsafe.Pointer(p)),
		genericRead,
		uintptr(syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE),
		0,
		openExisting,
		fileFlagBackupSemantics|fileFlagOpenReparsePoint,
		0,
	)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return "", false, fmt.Errorf("open %q: %w", path, errno)
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	buf := make([]byte, maxReparseDataSize)
	var returned uint32
	r, _, errno := procDeviceIoControl.Call(
		h,
		fsctlGetReparsePoint,
		0,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&returned)),
		0,
	)
	if r == 0 {
		// Not a reparse point (ERROR_NOT_A_REPARSE_POINT) is a normal answer.
		if errno == syscall.Errno(4390) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("FSCTL_GET_REPARSE_POINT on %q: %w", path, errno)
	}
	if len(buf) < 16 {
		return "", false, errors.New("short reparse buffer")
	}
	if binary.LittleEndian.Uint32(buf[0:]) != ioReparseTagMountPoint {
		return "", false, nil
	}
	subOff := binary.LittleEndian.Uint16(buf[8:])
	subLen := binary.LittleEndian.Uint16(buf[10:])
	start := 16 + int(subOff)
	if start+int(subLen) > len(buf) {
		return "", false, errors.New("corrupt reparse buffer")
	}
	u := make([]uint16, int(subLen)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(buf[start+i*2:])
	}
	t := string(utf16.Decode(u))
	t = strings.TrimPrefix(t, `\??\`)
	t = strings.TrimPrefix(t, `\\?\`)
	if strings.HasPrefix(t, `UNC\`) {
		t = `\\` + strings.TrimPrefix(t, `UNC\`)
	}
	return t, true, nil
}

func (windowsPlatform) ResolvePath(path string) (string, error) {
	return resolveViaReadLink(windowsPlatform{}, path)
}

func (windowsPlatform) RemoveLink(path string) error {
	_, isLink, err := current.ReadLink(path)
	if err != nil {
		return err
	}
	if !isLink {
		return ErrNotLink
	}
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return err
	}
	r, _, errno := procRemoveDirectoryW.Call(uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return fmt.Errorf("remove link %q: %w", path, errno)
	}
	return nil
}

func (windowsPlatform) FreeSpace(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	r, _, errno := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx(%q): %w", path, errno)
	}
	return free, nil
}

func (windowsPlatform) VolumeID(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, syscall.MAX_PATH)
	p, _ := syscall.UTF16PtrFromString(longPath(abs))
	r, _, errno := procGetVolumePathNameW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if r == 0 {
		return "", fmt.Errorf("GetVolumePathName(%q): %w", path, errno)
	}
	root := syscall.UTF16ToString(buf)

	volName := make([]uint16, 300)
	var serial, maxComp, flags uint32
	rootPtr, _ := syscall.UTF16PtrFromString(root)
	r, _, errno = procGetVolumeInformationW.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&volName[0])),
		uintptr(len(volName)),
		uintptr(unsafe.Pointer(&serial)),
		uintptr(unsafe.Pointer(&maxComp)),
		uintptr(unsafe.Pointer(&flags)),
		0, 0,
	)
	if r == 0 {
		return "", fmt.Errorf("GetVolumeInformation(%q): %w", root, errno)
	}
	return fmt.Sprintf("%s:%08X", strings.ToUpper(root), serial), nil
}

func (windowsPlatform) FindProcesses(names, cmdlineContains []string) ([]ProcInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("enumerate processes: %w", err)
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.ToLower(filepath.Base(n))] = true
	}
	var out []ProcInfo
	for _, p := range procs {
		name, err := p.Name()
		if err != nil || name == "" {
			continue
		}
		if !want[strings.ToLower(name)] {
			continue
		}
		cmd, _ := p.Cmdline()
		if len(cmdlineContains) > 0 && !containsAnyFold(cmd, cmdlineContains) {
			continue
		}
		out = append(out, ProcInfo{PID: p.Pid, Name: name, Cmdline: cmd})
	}
	return out, nil
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

func (windowsPlatform) TempEnvVars() []string { return []string{"TEMP", "TMP", "TMPDIR"} }

func (windowsPlatform) IsProcessAlive(pid int32) bool {
	ok, err := process.PidExists(pid)
	return err == nil && ok
}

func (windowsPlatform) ConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "midas")
	}
	return filepath.Join(os.Getenv("APPDATA"), "midas")
}

func (windowsPlatform) PathTokens() map[string]string {
	home, _ := os.UserHomeDir()
	appdata := os.Getenv("APPDATA")
	local := os.Getenv("LOCALAPPDATA")
	return map[string]string{
		"home":         home,
		"appdata":      appdata,
		"localappdata": local,
		"xdg_config":   appdata,
		"xdg_data":     local,
		"xdg_cache":    filepath.Join(local, "cache"),
		"xdg_state":    local,
	}
}
