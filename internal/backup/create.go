package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
)

// Reporter receives progress in bytes. *ui.Progress satisfies it.
type Reporter interface {
	Add(n int64)
}

// CreateOptions controls backup set creation.
type CreateOptions struct {
	App          string
	Target       string
	OriginalPath string
	Mode         string
	Rule         apps.BackupRule
	HashVerify   bool
	Progress     Reporter
}

// Estimate is the pre-flight size of a backup.
type Estimate struct {
	Bytes        int64
	Files        int
	SkippedFiles int
	SkippedBytes int64
}

const copyBufferSize = 1 << 20

// copyFileHook exists only so tests can mutate the open source between the
// before-Stat and the copy loop deterministically. It is nil in production.
var copyFileHook func(src string)

// EstimateBackup walks the source without changing anything.
func EstimateBackup(src, mode string, rule apps.BackupRule) (Estimate, error) {
	var est Estimate
	sel := newSelector(mode, rule)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read %q: %w", p, err)
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return rerr
		}
		if _, isLink, lerr := platform.Current().ReadLink(p); lerr != nil {
			return fmt.Errorf("read link %q: %w", p, lerr)
		} else if isLink {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %q: %w", p, ierr)
		}
		inc, reason := sel.decide(rel, fi.Size())
		if inc {
			est.Files++
			est.Bytes += fi.Size()
		} else if reason != "" {
			est.SkippedFiles++
			est.SkippedBytes += fi.Size()
		}
		return nil
	})
	return est, err
}

// Create produces a fully verified backup set or no set at all.
func (m *Manager) Create(opts CreateOptions, rp manifest.RestorePoint) (*Set, error) {
	if opts.Mode == ModeNone {
		return nil, errors.New("backup mode none cannot create a set")
	}
	src := opts.OriginalPath
	fi, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("backup source %q: %w", src, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("backup source %q is not a directory", src)
	}

	id := m.NewID(opts.App)
	operationID, err := newOperationID()
	if err != nil {
		return nil, fmt.Errorf("create backup operation ID: %w", err)
	}
	dir := m.SetDir(opts.App, id)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return nil, err
	}
	s := &Set{
		Schema:       SchemaVersion,
		ID:           id,
		OperationID:  operationID,
		App:          opts.App,
		Target:       opts.Target,
		OriginalPath: src,
		Mode:         opts.Mode,
		State:        StateCreating,
		CreatedAt:    time.Now().UTC(),
		Files:        []File{},
		Skipped:      []Skipped{},
		RestorePoint: rp,
		Dir:          dir,
	}
	if err := s.Save(); err != nil {
		removeTree(dir)
		return nil, err
	}
	host, _ := os.Hostname()
	lock, _ := json.Marshal(LockFile{PID: int32(os.Getpid()), Hostname: host, StartedAt: time.Now().UTC()})
	if err := writeAtomic(s.LockPath(), lock, 0o600); err != nil {
		removeTree(dir)
		return nil, err
	}

	if err := m.fill(s, opts); err != nil {
		removeTree(dir)
		return nil, fmt.Errorf("backup failed, nothing was changed: %w", err)
	}

	if err := verifySet(s, opts.Mode == ModeEssential || opts.HashVerify); err != nil {
		removeTree(dir)
		return nil, fmt.Errorf("backup verification failed, nothing was changed: %w", err)
	}

	s.State = StateVerified
	if err := protectTree(s); err != nil {
		removeTree(dir)
		return nil, fmt.Errorf("protect backup set: %w", err)
	}
	if err := s.Save(); err != nil {
		removeTree(dir)
		return nil, err
	}
	_ = os.Remove(s.LockPath())
	return s, nil
}

// checkCaseCollision records rel (already slash-separated) in seen and reports
// a collision when a different source path folds to the same case-insensitive
// key. Every entry registers — directories, links and regular files — because
// the backup set's on-disk layout can collide whichever kind produced the name.
// Excluded entries register too: their directories still exist in the set.
func checkCaseCollision(seen map[string]string, rel string) error {
	key := strings.ToLower(rel)
	if other, ok := seen[key]; ok && other != rel {
		return fmt.Errorf("case collision: %q and %q differ only by case", other, rel)
	}
	seen[key] = rel
	return nil
}

func (m *Manager) fill(s *Set, opts CreateOptions) error {
	src := opts.OriginalPath
	sel := newSelector(opts.Mode, opts.Rule)
	seen := make(map[string]string)

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read %q: %w", p, err)
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			if !d.IsDir() {
				return nil
			}
			fi, ierr := d.Info()
			if ierr != nil {
				return fmt.Errorf("stat %q: %w", p, ierr)
			}
			s.Dirs = append(s.Dirs, Dir{Path: ".", Mode: uint32(fi.Mode().Perm()), MTime: fi.ModTime().UTC()})
			if err := os.MkdirAll(s.FilesDir(), 0o700); err != nil {
				return err
			}
			return nil
		}
		// BACKUP.json stores slash-separated paths on every platform so
		// metadata stays portable and passes path validation.
		rel = filepath.ToSlash(rel)
		// Register before link/dir/selector handling: a directory or a link
		// that differs only by case would otherwise merge on disk.
		if cerr := checkCaseCollision(seen, rel); cerr != nil {
			return cerr
		}

		// Links are recreated, never followed. Junctions on Windows are not
		// reported as ModeSymlink by Lstat, so ask the platform layer.
		target, isLink, lerr := platform.Current().ReadLink(p)
		if lerr != nil {
			return fmt.Errorf("read link %q: %w", p, lerr)
		}
		if isLink {
			if err := m.saveLink(s, rel, p, target, d); err != nil {
				return err
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			fi, ierr := d.Info()
			if ierr != nil {
				return fmt.Errorf("stat %q: %w", p, ierr)
			}
			s.Dirs = append(s.Dirs, Dir{Path: rel, Mode: uint32(fi.Mode().Perm()), MTime: fi.ModTime().UTC()})
			// Create the directory in the set so empty directories are part of
			// it and a restore recreates them.
			if err := os.MkdirAll(filepath.Join(s.FilesDir(), rel), 0o700); err != nil {
				return err
			}
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %q: %w", p, ierr)
		}
		if !fi.Mode().IsRegular() {
			s.Skipped = append(s.Skipped, Skipped{Path: rel, Size: fi.Size(), Reason: "special file (not regular)"})
			return nil
		}
		inc, reason := sel.decide(rel, fi.Size())
		if !inc {
			if reason != "" {
				s.Skipped = append(s.Skipped, Skipped{Path: rel, Size: fi.Size(), Reason: reason})
			}
			return nil
		}
		f, n, cerr := copyFile(p, filepath.Join(s.FilesDir(), rel), fi)
		if cerr != nil {
			return cerr
		}
		f.Path = rel
		s.Files = append(s.Files, f)
		if opts.Progress != nil {
			opts.Progress.Add(n)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.Save()
}

func (m *Manager) saveLink(s *Set, rel, src, target string, d fs.DirEntry) error {
	fi, err := d.Info()
	if err != nil {
		return fmt.Errorf("stat link %q: %w", src, err)
	}
	dest := filepath.Join(s.FilesDir(), rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(filepath.Dir(src), target)
	}
	var linkErr error
	linkIsDir := d.IsDir() || platform.IsDirectoryLink(src)
	if linkIsDir {
		linkErr = platform.Current().CreateLink(dest, abs)
	} else {
		linkErr = os.Symlink(target, dest)
	}
	if linkErr != nil {
		// A link can fail on Windows without elevation. The shape is still
		// recorded, and the limitation is reported rather than hidden.
		s.Skipped = append(s.Skipped, Skipped{Path: rel, Reason: "link could not be recreated: " + linkErr.Error()})
		return nil
	}
	// Junctions report their target as an absolute path; store the same form
	// so verification compares like with like. File symlinks are stored
	// verbatim.
	stored := target
	if linkIsDir {
		stored = abs
	}
	s.Files = append(s.Files, File{
		Path:  rel,
		Size:  0,
		MTime: fi.ModTime().UTC(),
		Mode:  uint32(fi.Mode().Perm()),
		Link:  stored,
	})
	return nil
}

func copyFile(src, dst string, fi os.FileInfo) (File, int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return File{}, 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return File{}, 0, fmt.Errorf("open %q for backup: %w", src, err)
	}
	defer in.Close()

	before, err := in.Stat()
	if err != nil {
		return File{}, 0, fmt.Errorf("stat %q before backup: %w", src, err)
	}
	if !os.SameFile(fi, before) || before.Size() != fi.Size() || !before.ModTime().Equal(fi.ModTime()) {
		return File{}, 0, fmt.Errorf("source %q changed before it could be backed up", src)
	}
	if copyFileHook != nil {
		copyFileHook(src)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return File{}, 0, fmt.Errorf("create backup copy of %q: %w", src, err)
	}
	h := sha256.New()
	buf := make([]byte, copyBufferSize)
	n, err := io.CopyBuffer(io.MultiWriter(out, h), in, buf)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return File{}, n, fmt.Errorf("copy %q: %w", src, err)
	}
	after, err := in.Stat()
	if err != nil {
		return File{}, n, fmt.Errorf("stat %q after backup: %w", src, err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || n != before.Size() {
		return File{}, n, fmt.Errorf("source %q changed during backup; the set is discarded", src)
	}
	if err := os.Chtimes(dst, fi.ModTime(), fi.ModTime()); err != nil {
		return File{}, n, fmt.Errorf("set timestamp on backup copy of %q: %w", src, err)
	}
	return File{
		Size:   n,
		MTime:  fi.ModTime().UTC(),
		Mode:   uint32(fi.Mode().Perm()),
		SHA256: hex.EncodeToString(h.Sum(nil)),
	}, n, nil
}

func verifySet(s *Set, hash bool) error {
	if err := s.Validate(); err != nil {
		return err
	}
	var problems []string
	expected := make(map[string]bool, len(s.Files))
	for _, f := range s.Files {
		expected[f.Path] = true
	}
	walkErr := filepath.WalkDir(s.FilesDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("inspect backup path %q: %w", p, err)
		}
		if p == s.FilesDir() || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.FilesDir(), p)
		if err != nil {
			return err
		}
		if !expected[filepath.ToSlash(rel)] {
			problems = append(problems, filepath.ToSlash(rel)+": unexpected entry")
		}
		return nil
	})
	if walkErr != nil {
		problems = append(problems, walkErr.Error())
	}
	for _, f := range s.Files {
		p := filepath.Join(s.FilesDir(), f.Path)
		if f.Link != "" {
			t, isLink, err := platform.Current().ReadLink(p)
			if err != nil || !isLink || t != f.Link {
				problems = append(problems, fmt.Sprintf("%s: link missing or changed", f.Path))
			}
			continue
		}
		fi, err := os.Lstat(p)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: missing", f.Path))
			continue
		}
		if fi.Size() != f.Size {
			problems = append(problems, fmt.Sprintf("%s: size %d, expected %d", f.Path, fi.Size(), f.Size))
			continue
		}
		if hash {
			got, err := hashFile(p)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", f.Path, err))
				continue
			}
			if got != f.SHA256 {
				problems = append(problems, fmt.Sprintf("%s: hash mismatch", f.Path))
			}
		}
	}
	if len(problems) > 0 {
		shown := problems
		if len(shown) > 10 {
			shown = shown[:10]
		}
		return fmt.Errorf("%d file(s) failed verification:\n  %s", len(problems), strings.Join(shown, "\n  "))
	}
	return nil
}

// Verify re-checks a set's hashes (used by `backup verify`).
func Verify(s *Set) error { return verifySet(s, true) }

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, copyBufferSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func protectTree(s *Set) error {
	return filepath.WalkDir(s.FilesDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if _, isLink, lerr := platform.Current().ReadLink(p); lerr != nil {
			return fmt.Errorf("inspect backup path %q: %w", p, lerr)
		} else if isLink {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.Chmod(p, 0o500)
		}
		return os.Chmod(p, 0o400)
	})
}

// selector decides what essential mode covers.
type selector struct {
	mode string
	rule apps.BackupRule
}

func newSelector(mode string, rule apps.BackupRule) selector {
	return selector{mode: mode, rule: rule}
}

// decide returns include and, when not included, a reason to record.
func (s selector) decide(rel string, size int64) (bool, string) {
	relSlash := filepath.ToSlash(rel)
	if s.mode == ModeFull {
		return true, ""
	}
	if apps.MatchAny(s.rule.Exclude, relSlash) {
		return false, "excluded by [backup].exclude"
	}
	if s.rule.MaxFileMB > 0 && size > s.rule.MaxFileMB*1024*1024 {
		return false, fmt.Sprintf("larger than max_file_mb (%d MB)", s.rule.MaxFileMB)
	}
	if len(s.rule.Include) == 0 || apps.MatchAny(s.rule.Include, relSlash) {
		return true, ""
	}
	return false, "" // outside essential scope; counted as not covered
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// removeTree is the internal cleanup used when a set was never verified.
// Exported deletion still goes only through Delete.
func removeTree(root string) {
	_ = removeTreeNoFollow(root)
}
