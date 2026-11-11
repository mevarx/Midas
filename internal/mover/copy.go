package mover

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/midas-cli/midas/internal/platform"
)

// Reporter receives copied byte counts. *ui.Progress satisfies it.
type Reporter interface {
	Add(n int64)
}

// ErrVerifyFailed marks verification failures (exit code 6).
var ErrVerifyFailed = errors.New("verification failed")

// Stats summarizes a tree.
type Stats struct {
	Files int
	Dirs  int
	Bytes int64
}

// CopyOptions controls CopyTree.
type CopyOptions struct {
	Progress Reporter
}

const copyBufSize = 1 << 20

// CopyTree copies src to dst, preserving structure, links (recreated, never
// followed), modes and timestamps. dst must not already exist.
func CopyTree(src, dst string, opts CopyOptions) (Stats, error) {
	var st Stats
	fi, err := os.Lstat(src)
	if err != nil {
		return st, err
	}
	if !fi.IsDir() {
		return st, fmt.Errorf("%q is not a directory", src)
	}
	if _, err := os.Lstat(dst); err == nil {
		return st, fmt.Errorf("destination %q already exists", dst)
	}
	if err := os.MkdirAll(dst, fi.Mode().Perm()); err != nil {
		return st, err
	}
	st.Dirs++

	seen := make(map[string]string)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read %q: %w", p, err)
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return rerr
		}
		target := filepath.Join(dst, rel)

		if lt, isLink, lerr := platform.Current().ReadLink(p); lerr == nil && isLink {
			if err := recreateLink(p, target, lt, d); err != nil {
				return err
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		} else if lerr != nil && d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("read link %q: %w", p, lerr)
		}

		info, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %q: %w", p, ierr)
		}
		if d.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			_ = os.Chtimes(target, info.ModTime(), info.ModTime())
			st.Dirs++
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot copy special file %q", p)
		}
		lower := strings.ToLower(filepath.ToSlash(rel))
		if other, ok := seen[lower]; ok && other != filepath.ToSlash(rel) {
			return fmt.Errorf("case collision between %q and %q; aborting before any data is touched", other, filepath.ToSlash(rel))
		}
		seen[lower] = filepath.ToSlash(rel)

		n, err := copyRegular(p, target, info)
		if err != nil {
			return err
		}
		st.Files++
		st.Bytes += n
		if opts.Progress != nil {
			opts.Progress.Add(n)
		}
		return nil
	})
	return st, err
}

func recreateLink(src, dst, target string, d fs.DirEntry) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(filepath.Dir(src), target)
	}
	if d.IsDir() {
		return platform.Current().CreateLink(dst, abs)
	}
	return os.Symlink(target, dst)
}

func copyRegular(src, dst string, fi os.FileInfo) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %q: %w (locked or unreadable; nothing was removed)", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", dst, err)
	}
	n, err := io.CopyBuffer(out, in, make([]byte, copyBufSize))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("copy %q: %w", src, err)
	}
	if err := os.Chtimes(dst, fi.ModTime(), fi.ModTime()); err != nil {
		return n, err
	}
	return n, nil
}

// VerifyTree compares src and dst by count, size and (optionally) hash.
// It never follows links.
func VerifyTree(src, dst string, hash bool) (Stats, error) {
	var st Stats
	var problems []string
	srcCount := 0
	dstCount := 0

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read source %q: %w", p, err)
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return rerr
		}
		srcCount++
		if d.IsDir() {
			st.Dirs++
		}
		tp := filepath.Join(dst, rel)

		if lt, isLink, lerr := platform.Current().ReadLink(p); lerr == nil && isLink {
			dt, isDstLink, derr := platform.Current().ReadLink(tp)
			if derr != nil || !isDstLink || !sameLinkTarget(lt, dt, filepath.Dir(p)) {
				problems = append(problems, rel+": link missing or pointing elsewhere")
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		si, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat source %q: %w", p, ierr)
		}
		if d.IsDir() {
			return nil
		}
		st.Files++
		st.Bytes += si.Size()
		di, derr := os.Lstat(tp)
		if derr != nil {
			problems = append(problems, rel+": missing in destination")
			return nil
		}
		if di.Size() != si.Size() {
			problems = append(problems, fmt.Sprintf("%s: size %d, expected %d", rel, di.Size(), si.Size()))
			return nil
		}
		if hash {
			h1, err1 := hashFile(p)
			h2, err2 := hashFile(tp)
			if err1 != nil || err2 != nil {
				problems = append(problems, fmt.Sprintf("%s: cannot hash (%v / %v)", rel, err1, err2))
			} else if h1 != h2 {
				problems = append(problems, rel+": hash mismatch")
			}
		}
		return nil
	})
	if err != nil {
		return st, err
	}

	_ = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dst {
			return nil
		}
		dstCount++
		return nil
	})
	if dstCount != srcCount {
		problems = append(problems, fmt.Sprintf("entry count mismatch: source %d, destination %d", srcCount, dstCount))
	}
	if len(problems) > 0 {
		shown := problems
		if len(shown) > 10 {
			shown = shown[:10]
		}
		return st, fmt.Errorf("verification failed (%d problem(s)):\n  %s", len(problems), strings.Join(shown, "\n  "))
	}
	return st, nil
}

func sameLinkTarget(a, b, aDir string) bool {
	resolve := func(t, dir string) string {
		if !filepath.IsAbs(t) {
			t = filepath.Join(dir, t)
		}
		return filepath.Clean(t)
	}
	return platform.SamePath(resolve(a, aDir), resolve(b, aDir))
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, copyBufSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HealthCheck verifies that original is a Midas link pointing at dest and
// that dest is a readable directory.
func HealthCheck(original, dest string) (healthy bool, detail string) {
	target, isLink, err := platform.Current().ReadLink(original)
	if err != nil {
		return false, "cannot inspect link: " + err.Error()
	}
	if !isLink {
		return false, "original path is not a link"
	}
	same := platform.SamePath(filepath.Clean(target), filepath.Clean(dest))
	if !same {
		return false, fmt.Sprintf("link points to %s, expected %s", target, dest)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		return false, "link target is not reachable: " + err.Error()
	}
	if !fi.IsDir() {
		return false, "link target is not a directory"
	}
	if _, err := os.ReadDir(dest); err != nil {
		return false, "link target is not readable: " + err.Error()
	}
	// Read one file through the link, exactly as an app would.
	if entries, err := os.ReadDir(dest); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			f, err := os.Open(filepath.Join(original, e.Name()))
			if err != nil {
				return false, "cannot read a file through the link: " + err.Error()
			}
			buf := make([]byte, 1)
			_, _ = f.Read(buf)
			f.Close()
			break
		}
	}
	return true, ""
}

// PathExists reports whether p exists (link or not).
func PathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// SameVolume reports whether two paths live on the same volume.
func SameVolume(a, b string) (bool, error) {
	va, err := platform.Current().VolumeID(a)
	if err != nil {
		return false, err
	}
	vb, err := platform.Current().VolumeID(b)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(va, vb), nil
}

// RemoveAllOwned removes a Midas-owned scratch directory (partial copies).
// It is never used on backup sets or user data.
func RemoveAllOwned(p string) error {
	err := os.RemoveAll(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
