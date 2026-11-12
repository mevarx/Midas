package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mevarx/Midas/internal/manifest"
)

// TestCreateDiscardsSetWhenSourceChangesDuringCopy makes the source grow
// between the before-Stat and the copy loop. Full mode must fail the whole set
// and remove it, leaving no partial backup behind.
func TestCreateDiscardsSetWhenSourceChangesDuringCopy(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, "data.txt")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	mutated := false
	copyFileHook = func(src string) {
		if src != file || mutated {
			return
		}
		mutated = true
		f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Errorf("open source for mutation: %v", err)
			return
		}
		defer f.Close()
		if _, err := f.WriteString("-appended"); err != nil {
			t.Errorf("mutate source: %v", err)
		}
	}
	t.Cleanup(func() { copyFileHook = nil })

	root := t.TempDir()
	m := NewManager(root)
	_, err := m.Create(CreateOptions{
		App:          "app",
		Target:       "target",
		OriginalPath: source,
		Mode:         ModeFull,
	}, manifest.RestorePoint{})
	if err == nil {
		t.Fatal("Create succeeded after the source changed during the copy")
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Fatalf("Create error does not report the changed source: %v", err)
	}

	appDir := filepath.Join(root, "app")
	entries, rerr := os.ReadDir(appDir)
	if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("backup set was left behind after the failure: %v", entries)
	}
}

// TestCheckCaseCollisionDirectoryVsDirectory proves fill's registration
// rejects two directories differing only by case without relying on the
// filesystem holding both names.
func TestCheckCaseCollisionDirectoryVsDirectory(t *testing.T) {
	seen := make(map[string]string)
	if err := checkCaseCollision(seen, "Foo"); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := checkCaseCollision(seen, "foo")
	if err == nil {
		t.Fatal("directory-vs-directory case collision was not detected")
	}
	for _, want := range []string{`"Foo"`, `"foo"`, "differ only by case"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("collision error %q does not contain %q", err, want)
		}
	}
	if err := checkCaseCollision(seen, "Foo"); err != nil {
		t.Fatalf("repeated identical path was rejected: %v", err)
	}
}

// TestCheckCaseCollisionLinkVsFile covers a link and a regular file folding to
// the same key.
func TestCheckCaseCollisionLinkVsFile(t *testing.T) {
	seen := make(map[string]string)
	if err := checkCaseCollision(seen, "Link"); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if err := checkCaseCollision(seen, "link"); err == nil {
		t.Fatal("link-vs-file case collision was not detected")
	}
}

func TestCheckCaseCollisionNestedPaths(t *testing.T) {
	seen := make(map[string]string)
	if err := checkCaseCollision(seen, "dir/Foo"); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := checkCaseCollision(seen, "dir/foo")
	if err == nil {
		t.Fatal("nested case collision was not detected")
	}
	if !strings.Contains(err.Error(), "dir/Foo") || !strings.Contains(err.Error(), "dir/foo") {
		t.Fatalf("collision error does not name both paths: %q", err)
	}
	if err := checkCaseCollision(seen, "other/foo"); err != nil {
		t.Fatalf("distinct parent directory was rejected: %v", err)
	}
}
