//go:build !windows

package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mevarx/Midas/internal/manifest"
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

// TestCreateAbortsOnCaseCollision builds a real directory and a regular file
// that differ only by case and checks Create fails and discards the set.
func TestCreateAbortsOnCaseCollision(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if !caseSensitiveFS(t, source) {
		t.Skip("filesystem is case-insensitive")
	}
	if err := os.MkdirAll(filepath.Join(source, "Foo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "foo"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	m := NewManager(root)
	_, err := m.Create(CreateOptions{
		App:          "app",
		Target:       "target",
		OriginalPath: source,
		Mode:         ModeFull,
	}, manifest.RestorePoint{})
	if err == nil {
		t.Fatal("Create accepted a directory/file case collision")
	}
	if !strings.Contains(err.Error(), `"Foo"`) || !strings.Contains(err.Error(), `"foo"`) {
		t.Fatalf("Create error does not name both paths: %v", err)
	}

	appDir := filepath.Join(root, "app")
	entries, rerr := os.ReadDir(appDir)
	if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("backup set was left behind after the collision: %v", entries)
	}
}
