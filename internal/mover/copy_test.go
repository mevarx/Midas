package mover

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyTreePreservesDirectoryTimestamps(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dirs := []string{
		src,
		filepath.Join(src, "sub"),
		filepath.Join(src, "sub", "deeper"),
		filepath.Join(src, "other"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := [][2]string{
		{filepath.Join(src, "a.txt"), "alpha"},
		{filepath.Join(src, "sub", "b.txt"), "bravo"},
		{filepath.Join(src, "sub", "deeper", "c.txt"), "charlie"},
		{filepath.Join(src, "other", "extra.txt"), "delta"},
	}
	for _, f := range files {
		if err := os.WriteFile(f[0], []byte(f[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Set every directory a couple of seconds in the past, deepest first so the
	// final parent write is not disturbed by later child writes.
	past := time.Now().Add(-2 * time.Second)
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chtimes(dirs[i], past, past); err != nil {
			t.Fatal(err)
		}
	}
	// Source mtimes are read back from the filesystem: some filesystems round
	// timestamps, so the requested time is not necessarily what was stored.
	srcTimes := make(map[string]time.Time, len(dirs))
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		srcTimes[d] = fi.ModTime()
	}

	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			dst := filepath.Join(root, fmt.Sprintf("dst-%d", workers))
			if _, err := CopyTree(src, dst, CopyOptions{Workers: workers}); err != nil {
				t.Fatal(err)
			}
			for _, d := range dirs {
				rel, err := filepath.Rel(src, d)
				if err != nil {
					t.Fatal(err)
				}
				fi, err := os.Stat(filepath.Join(dst, rel))
				if err != nil {
					t.Fatalf("stat copied directory %q: %v", rel, err)
				}
				want := srcTimes[d]
				if diff := fi.ModTime().Sub(want); diff > 2*time.Second || diff < -2*time.Second {
					t.Errorf("directory %q mtime = %v, source %v (difference %v)", rel, fi.ModTime(), want, diff)
				}
			}
			for _, f := range files {
				rel, err := filepath.Rel(src, f[0])
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(dst, rel))
				if err != nil {
					t.Fatalf("read copied file %q: %v", rel, err)
				}
				if string(got) != f[1] {
					t.Errorf("file %q = %q, want %q", rel, got, f[1])
				}
			}
		})
	}
}

// TestCheckCaseCollisionDirectoryVsDirectory proves that two directories whose
// names differ only by case are rejected. The collision map receives only the
// paths; the filesystem is never asked to hold both names, so this runs on
// every platform.
func TestCheckCaseCollisionDirectoryVsDirectory(t *testing.T) {
	seen := make(map[string]string)
	if err := checkCaseCollision(seen, "Foo"); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := checkCaseCollision(seen, "foo")
	if err == nil {
		t.Fatal("directory-vs-directory case collision was not detected")
	}
	for _, want := range []string{`"Foo"`, `"foo"`, "original untouched; partial preserved"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("collision error %q does not contain %q", err, want)
		}
	}
	// A rebuild revisits identical paths; that must stay allowed.
	if err := checkCaseCollision(seen, "Foo"); err != nil {
		t.Fatalf("repeated identical path was rejected: %v", err)
	}
}

// TestCheckCaseCollisionLinkVsFile covers a link and a regular file that fold
// to the same key, which the old file-only check could not see for the link.
func TestCheckCaseCollisionLinkVsFile(t *testing.T) {
	seen := make(map[string]string)
	if err := checkCaseCollision(seen, "Link"); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := checkCaseCollision(seen, "link")
	if err == nil {
		t.Fatal("link-vs-file case collision was not detected")
	}
	for _, want := range []string{`"Link"`, `"link"`, "original untouched; partial preserved"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("collision error %q does not contain %q", err, want)
		}
	}
}

// TestCheckCaseCollisionNestedPaths checks the comparison happens per full
// relative path, not per base name.
func TestCheckCaseCollisionNestedPaths(t *testing.T) {
	seen := make(map[string]string)
	if err := checkCaseCollision(seen, filepath.Join("dir", "Foo")); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := checkCaseCollision(seen, filepath.Join("dir", "foo"))
	if err == nil {
		t.Fatal("nested case collision was not detected")
	}
	if !strings.Contains(err.Error(), "dir/Foo") || !strings.Contains(err.Error(), "dir/foo") {
		t.Fatalf("collision error does not use slash-separated paths: %q", err)
	}
	// Same base name in a different directory is not a collision.
	if err := checkCaseCollision(seen, filepath.Join("other", "foo")); err != nil {
		t.Fatalf("distinct parent directory was rejected: %v", err)
	}
}
