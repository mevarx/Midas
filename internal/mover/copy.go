package mover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mevarx/Midas/internal/platform"
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
	Context  context.Context
	Workers  int
	// StageWritable creates copied files and directories with
	// owner-writable modes instead of the source's permissions. Use when
	// staging a restore from a read-only-protected tree such as a backup
	// set; the caller applies the recorded final modes after verification.
	StageWritable bool
}

type copyTask struct {
	source string
	dest   string
	info   os.FileInfo
	perm   os.FileMode
}

const copyBufSize = 1 << 20

// CopyTree copies src to dst, preserving structure, modes, file and directory
// timestamps, and links (recreated, never followed). Directory timestamps are
// applied after their children are written, so they match the source. dst must
// not already exist.
func CopyTree(src, dst string, opts CopyOptions) (Stats, error) {
	var st Stats
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
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
	rootMode := fi.Mode().Perm()
	if opts.StageWritable {
		rootMode = 0o700
	}
	if err := os.MkdirAll(dst, rootMode); err != nil {
		return st, err
	}
	st.Dirs++
	// Directory timestamps are applied after the walk: writing children bumps
	// the parent mtime, so restoring it must happen bottom-up at the end.
	dirTimes := []dirTime{{path: dst, mtime: fi.ModTime()}}

	if opts.Workers < 0 || opts.Workers > 16 {
		return st, errors.New("copy workers must be between 1 and 16")
	}
	if opts.Workers == 0 {
		opts.Workers = 1
	}
	var pool *copyPool
	if opts.Workers > 1 {
		pool, err = startCopyPool(ctx, opts.Workers, opts.Progress)
		if err != nil {
			return st, err
		}
	}
	seen := make(map[string]string)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if pool != nil {
			if failure := pool.failure(); failure != nil {
				return failure
			}
			if pool.ctx.Err() != nil {
				return pool.ctx.Err()
			}
		}
		if err != nil {
			return fmt.Errorf("read %q: %w", p, err)
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return rerr
		}
		target := filepath.Join(dst, rel)
		// Register before any type handling so a name that differs only by
		// case can never silently merge into the tree being built.
		if cerr := checkCaseCollision(seen, rel); cerr != nil {
			return cerr
		}

		lt, isLink, lerr := platform.Current().ReadLink(p)
		if lerr != nil {
			return fmt.Errorf("read link %q: %w", p, lerr)
		}
		if isLink {
			if err := recreateLink(src, dst, p, target, lt, d); err != nil {
				return err
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %q: %w", p, ierr)
		}
		if d.IsDir() {
			dirMode := info.Mode().Perm()
			if opts.StageWritable {
				dirMode = 0o700
			}
			if err := os.MkdirAll(target, dirMode); err != nil {
				return err
			}
			dirTimes = append(dirTimes, dirTime{path: target, mtime: info.ModTime()})
			st.Dirs++
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot copy special file %q", p)
		}
		perm := info.Mode().Perm()
		if opts.StageWritable {
			perm = 0o600
		}
		if pool != nil {
			return pool.submit(copyTask{source: p, dest: target, info: info, perm: perm})
		}
		n, err := copyRegular(ctx, p, target, info, perm)
		if err != nil {
			return err
		}
		st.Files++
		st.Bytes += n
		if opts.Progress != nil {
			opts.Progress.Add(n)
			if reporter, ok := opts.Progress.(interface{ AddFile() }); ok {
				reporter.AddFile()
			}
		}
		return nil
	})
	if pool != nil {
		totals, copyErr := pool.finish(err)
		st.Files += totals.files
		st.Bytes += totals.bytes
		err = copyErr
	}
	if err != nil {
		return st, err
	}
	if terr := applyDirTimes(dirTimes); terr != nil {
		return st, terr
	}
	return st, nil
}

// checkCaseCollision records rel in seen and reports a collision when a
// different source path already folded to the same case-insensitive key.
// Every entry type is registered (directories, links, regular files) because
// each creates a destination name. Repeating the exact same path is allowed:
// rebuilds revisit identical entries.
func checkCaseCollision(seen map[string]string, rel string) error {
	slash := filepath.ToSlash(rel)
	key := strings.ToLower(slash)
	if other, ok := seen[key]; ok && other != slash {
		return fmt.Errorf("case collision between %q and %q; original untouched; partial preserved", other, slash)
	}
	seen[key] = slash
	return nil
}

// dirTime is a destination directory whose source mtime must be restored once
// all of its children have been written.
type dirTime struct {
	path  string
	mtime time.Time
}

// applyDirTimes restores directory mtimes bottom-up: reverse lexical order puts
// every child before its parent and the root destination last, so a copy is not
// observed mid-restoration and the root keeps the source mtime.
func applyDirTimes(dirs []dirTime) error {
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].path > dirs[j].path })
	for _, d := range dirs {
		if err := os.Chtimes(d.path, d.mtime, d.mtime); err != nil {
			return fmt.Errorf("set directory timestamp %q: %w", d.path, err)
		}
	}
	return nil
}

// applyTreeModes restores the source tree's permission bits on a staged copy
// made with CopyOptions.StageWritable. Entries are chmod'ed bottom-up (reverse
// lexical order, which puts the root last) so a read-only directory never
// blocks changes to its children. Links are never chmod'ed.
func applyTreeModes(src, dst string) error {
	var paths []string
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read %q: %w", p, err)
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if _, isLink, lerr := platform.Current().ReadLink(p); lerr != nil {
			return fmt.Errorf("read link %q: %w", p, lerr)
		} else if isLink {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, rel := range paths {
		srcPath := filepath.Join(src, rel)
		dstPath := dst
		if rel != "." {
			dstPath = filepath.Join(dst, rel)
		}
		info, err := os.Lstat(srcPath)
		if err != nil {
			return fmt.Errorf("stat source %q: %w", srcPath, err)
		}
		if _, err := os.Lstat(dstPath); err != nil {
			return fmt.Errorf("stat staged copy %q: %w", dstPath, err)
		}
		if err := os.Chmod(dstPath, info.Mode().Perm()); err != nil {
			return fmt.Errorf("restore mode on %q: %w", dstPath, err)
		}
	}
	return nil
}

type copyTotals struct {
	files int
	bytes int64
}

type copyResult struct {
	index int
	bytes int64
	err   error
}

type indexedCopyTask struct {
	index int
	task  copyTask
}

type copyPool struct {
	ctx           context.Context
	callerCtx     context.Context
	cancel        context.CancelFunc
	jobs          chan indexedCopyTask
	results       chan copyResult
	workerGroup   sync.WaitGroup
	collectorDone chan struct{}
	mu            sync.Mutex
	firstErr      error
	firstErrIndex int
	totals        copyTotals
	nextIndex     int
	progress      Reporter
}

type lockedReporter struct {
	mu sync.Mutex
	r  Reporter
}

func (r *lockedReporter) Add(n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.r.Add(n)
}

func (r *lockedReporter) AddFile() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reporter, ok := r.r.(interface{ AddFile() }); ok {
		reporter.AddFile()
	}
}

func startCopyPool(ctx context.Context, workers int, progress Reporter) (*copyPool, error) {
	if workers < 1 {
		workers = 1
	}
	if workers > 16 {
		return nil, errors.New("copy workers must be between 1 and 16")
	}
	workCtx, cancel := context.WithCancel(ctx)
	pool := &copyPool{
		ctx: workCtx, callerCtx: ctx, cancel: cancel,
		jobs:          make(chan indexedCopyTask, workers*2),
		results:       make(chan copyResult, workers),
		collectorDone: make(chan struct{}),
		firstErrIndex: int(^uint(0) >> 1),
		progress:      progress,
	}
	reporter := progress
	if progress != nil && workers > 1 {
		reporter = &lockedReporter{r: progress}
	}
	pool.progress = reporter
	for range workers {
		pool.workerGroup.Add(1)
		go func() {
			defer pool.workerGroup.Done()
			for job := range pool.jobs {
				if err := pool.ctx.Err(); err != nil {
					pool.results <- copyResult{index: job.index, err: err}
					continue
				}
				n, err := copyRegular(pool.ctx, job.task.source, job.task.dest, job.task.info, job.task.perm)
				if err == nil && reporter != nil {
					reporter.Add(n)
					if fileReporter, ok := reporter.(interface{ AddFile() }); ok {
						fileReporter.AddFile()
					}
				}
				if err != nil {
					pool.recordError(job.index, err)
				}
				pool.results <- copyResult{index: job.index, bytes: n, err: err}
				if err != nil {
					pool.cancel()
				}
			}
		}()
	}
	go func() {
		defer close(pool.collectorDone)
		for result := range pool.results {
			if result.err != nil {
				continue
			}
			pool.mu.Lock()
			pool.totals.files++
			pool.totals.bytes += result.bytes
			pool.mu.Unlock()
		}
	}()
	return pool, nil
}

func (p *copyPool) addFile() {
	if reporter, ok := p.progress.(interface{ AddFile() }); ok {
		reporter.AddFile()
	}
}

func (p *copyPool) submit(task copyTask) error {
	index := p.nextIndex
	select {
	case p.jobs <- indexedCopyTask{index: index, task: task}:
		p.nextIndex++
		return nil
	case <-p.ctx.Done():
		if err := p.failure(); err != nil {
			return err
		}
		return p.ctx.Err()
	}
}

func (p *copyPool) recordError(index int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < p.firstErrIndex && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		p.firstErrIndex = index
		p.firstErr = err
	}
}

func (p *copyPool) failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.firstErr
}

func (p *copyPool) finish(walkErr error) (copyTotals, error) {
	if walkErr != nil {
		p.cancel()
	}
	close(p.jobs)
	p.workerGroup.Wait()
	close(p.results)
	<-p.collectorDone
	callerErr := p.callerCtx.Err()
	p.cancel()
	p.mu.Lock()
	totals, copyErr := p.totals, p.firstErr
	p.mu.Unlock()
	if copyErr != nil {
		return totals, fmt.Errorf("copy failed: %w", copyErr)
	}
	if callerErr != nil && walkErr == nil {
		walkErr = callerErr
	}
	return totals, walkErr
}

func recreateLink(srcRoot, dstRoot, src, dst, target string, d fs.DirEntry) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("refusing to replace existing link destination %q", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(src), resolved)
	}
	resolved, err := filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return err
	}
	linkTarget := resolved
	directoryTarget := resolved
	if inside, rel := pathWithinRoot(srcRoot, resolved); inside {
		mappedRoot, err := filepath.Abs(filepath.Clean(dstRoot))
		if err != nil {
			return err
		}
		mapped := filepath.Join(mappedRoot, rel)
		directoryTarget = mapped
		if !filepath.IsAbs(target) {
			linkTarget, err = filepath.Rel(filepath.Dir(dst), mapped)
			if err != nil {
				return err
			}
		} else {
			linkTarget = mapped
		}
	}
	linkIsDir := d.IsDir() || platform.IsDirectoryLink(src)
	if linkIsDir {
		return platform.Current().CreateLink(dst, directoryTarget)
	}
	return os.Symlink(linkTarget, dst)
}

func copyRegular(ctx context.Context, src, dst string, fi os.FileInfo, perm os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %q: %w (locked or unreadable; nothing was removed)", src, err)
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat %q before copy: %w", src, err)
	}
	if !os.SameFile(fi, before) || before.Size() != fi.Size() || !before.ModTime().Equal(fi.ModTime()) {
		return 0, fmt.Errorf("source %q changed before it could be copied", src)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return 0, fmt.Errorf("create %q: %w", dst, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()
	buf := make([]byte, copyBufSize)
	var n int64
	for {
		select {
		case <-ctx.Done():
			return n, fmt.Errorf("copy canceled for %q: %w", src, ctx.Err())
		default:
		}
		read, readErr := in.Read(buf)
		if read > 0 {
			written, writeErr := out.Write(buf[:read])
			n += int64(written)
			if writeErr != nil {
				return n, fmt.Errorf("copy %q: %w", src, writeErr)
			}
			if written != read {
				return n, fmt.Errorf("copy %q: %w", src, io.ErrShortWrite)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return n, fmt.Errorf("copy %q: %w", src, readErr)
		}
	}
	if err := out.Sync(); err != nil {
		return n, fmt.Errorf("sync %q: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		closed = true
		return n, err
	}
	closed = true
	after, err := in.Stat()
	if err != nil {
		return n, fmt.Errorf("stat %q after copy: %w", src, err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || n != before.Size() {
		return n, fmt.Errorf("source %q changed during copy; the incomplete destination was preserved", src)
	}
	if err := os.Chtimes(dst, fi.ModTime(), fi.ModTime()); err != nil {
		return n, err
	}
	return n, nil
}

// TreeSnapshot captures stable metadata for source-mutation detection. File
// content integrity remains the responsibility of VerifyTree's hash mode.
func TreeSnapshot(root string) (map[string]string, error) {
	if _, err := os.Lstat(root); err != nil {
		return nil, err
	}
	if !platform.IsRealDirectory(root) {
		return nil, fmt.Errorf("%q is not a real directory", root)
	}
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect source %q: %w", path, walkErr)
		}
		if path == root {
			return nil
		}
		// Links first: on Windows a junction reports as an irregular file,
		// not a directory, and must never fall through to the dir/file
		// branches below.
		if target, isLink, err := platform.Current().ReadLink(path); err != nil {
			return err
		} else if isLink {
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("source entry %q escapes %q", path, root)
			}
			out[filepath.ToSlash(rel)] = fmt.Sprintf("link:%s|%d|%d|%o", linkTargetIdentity(root, path, target), 0, 0, 0o777)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("source entry %q escapes %q", path, root)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		kind := "file"
		size := info.Size()
		mtime := info.ModTime().UnixNano()
		if info.IsDir() {
			kind, size, mtime = "dir", 0, 0
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported source type at %q", path)
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%s|%d|%d|%o", kind, size, mtime, info.Mode().Perm())
		return nil
	})
	return out, err
}

// SameTreeSnapshot reports whether two metadata snapshots match.
func SameTreeSnapshot(a, b map[string]string) bool { return reflect.DeepEqual(a, b) }

// VerifyTree compares src and dst by count, size and (optionally) hash.
// It never follows links.
func VerifyTree(src, dst string, hash bool) (Stats, error) {
	return VerifyTreeContext(context.Background(), src, dst, hash, nil)
}

// VerifyTreeContext compares two trees while supporting cancellation and a
// separate verification progress reporter.
func VerifyTreeContext(ctx context.Context, src, dst string, hash bool, progress Reporter) (Stats, error) {
	var st Stats
	if ctx == nil {
		ctx = context.Background()
	}
	var problems []string
	srcCount := 0
	dstCount := 0

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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

		lt, isLink, lerr := platform.Current().ReadLink(p)
		if lerr != nil {
			return fmt.Errorf("read link %q: %w", p, lerr)
		}
		if isLink {
			dt, isDstLink, derr := platform.Current().ReadLink(tp)
			if derr != nil || !isDstLink || !sameLinkTarget(lt, dt, src, dst, p, tp) {
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
		if progress != nil {
			progress.Add(si.Size())
			if fileReporter, ok := progress.(interface{ AddFile() }); ok {
				fileReporter.AddFile()
			}
		}
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
			h1, err1 := hashFileContext(ctx, p)
			h2, err2 := hashFileContext(ctx, tp)
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

func sameLinkTarget(a, b, srcRoot, dstRoot, srcPath, dstPath string) bool {
	return linkTargetIdentity(srcRoot, srcPath, a) == linkTargetIdentity(dstRoot, dstPath, b)
}

func linkTargetIdentity(root, linkPath, target string) string {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "invalid:" + target
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(linkPath), resolved)
	}
	resolved, err = filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return "invalid:" + target
	}
	if inside, rel := pathWithinRoot(rootAbs, resolved); inside {
		return "internal:" + filepath.ToSlash(rel)
	}
	return "external:" + resolved
}

func pathWithinRoot(root, candidate string) (bool, string) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false, ""
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return false, ""
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, ""
	}
	return true, rel
}

func hashFile(p string) (string, error) {
	return hashFileContext(context.Background(), p)
}

func hashFileContext(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, copyBufSize)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
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
	entries, err := os.ReadDir(dest)
	if err != nil {
		return false, "link target is not readable: " + err.Error()
	}
	// Read one file through the link, exactly as an app would. A vault with no
	// top-level file is still checked further down the tree; only a tree with
	// no regular file at all skips the read-through.
	rel, ok := healthCheckFile(dest, "", entries, 0)
	if !ok {
		return true, "link is readable; no regular file exists to read through the link"
	}
	through := filepath.Join(original, rel)
	f, err := os.Open(through)
	if err != nil {
		return false, fmt.Sprintf("cannot open %s through the link: %v", through, err)
	}
	buf := make([]byte, 1)
	_, readErr := f.Read(buf)
	_ = f.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, fmt.Sprintf("cannot read %s through the link: %v", through, readErr)
	}
	return true, ""
}

// healthCheckMaxDepth bounds the descent HealthCheck performs to find a file to
// read through the link, so the check stays cheap on a deep vault tree.
const healthCheckMaxDepth = 8

// healthCheckFile finds a regular file beneath dest, preferring entries of dir
// (the already-read listing of dest) over subdirectories, and returns its path
// relative to dest. Links are skipped so a link cycle can never be followed,
// and directories are descended only up to healthCheckMaxDepth.
func healthCheckFile(dir, rel string, entries []os.DirEntry, depth int) (string, bool) {
	type subdir struct{ full, rel string }
	var subdirs []subdir
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		child := e.Name()
		if rel != "" {
			child = filepath.Join(rel, e.Name())
		}
		if _, isLink, err := platform.Current().ReadLink(full); err != nil || isLink {
			continue
		}
		if e.IsDir() {
			subdirs = append(subdirs, subdir{full: full, rel: child})
			continue
		}
		if e.Type().IsRegular() {
			return child, true
		}
	}
	if depth >= healthCheckMaxDepth {
		return "", false
	}
	for _, sub := range subdirs {
		subEntries, err := os.ReadDir(sub.full)
		if err != nil {
			continue
		}
		if found, ok := healthCheckFile(sub.full, sub.rel, subEntries, depth+1); ok {
			return found, true
		}
	}
	return "", false
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
