//go:build !windows

package mover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caseSensitiveFS builds a probe file and reports whether an upper-cased
// lookup resolves to it. On case-insensitive filesystems a Foo/foo source
// cannot be built at all, so callers skip instead of assuming.
func caseSensitiveFS(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Lstat(filepath.Join(dir, "PROBE"))
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	return os.IsNotExist(statErr)
}

// TestCopyTreeAbortsOnCaseCollision builds a real directory and a regular file
// that differ only by case and checks the walk aborts instead of merging them.
func TestCopyTreeAbortsOnCaseCollision(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if !caseSensitiveFS(t, src) {
		t.Skip("filesystem is case-insensitive")
	}
	if err := os.MkdirAll(filepath.Join(src, "Foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "foo"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(root, "dst")
	_, err := CopyTree(src, dst, CopyOptions{})
	if err == nil {
		t.Fatal("CopyTree accepted a directory/file case collision")
	}
	if !strings.Contains(err.Error(), `case collision between "Foo" and "foo"`) {
		t.Fatalf("unexpected collision error: %v", err)
	}
	// The walk aborts at the colliding entry, so the staging tree never
	// receives it; CopyTree's caller commits only on success.
	entries, rerr := os.ReadDir(dst)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 1 || entries[0].Name() != "Foo" {
		t.Fatalf("staging tree holds %v, want only the first entry Foo", entries)
	}
}
