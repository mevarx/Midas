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
