package backup

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupMetadataRejectsTraversal(t *testing.T) {
	s := &Set{Schema: SchemaVersion, ID: "set-1", App: "app", Target: "config", Dir: filepath.Join(t.TempDir(), "set-1"), Files: []File{{Path: "../outside", Size: 1}}}
	if err := s.Validate(); err == nil {
		t.Fatal("traversal path was accepted")
	}
}

func TestVerifyDetectsHashMismatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "set-1")
	files := filepath.Join(dir, "files")
	if err := os.MkdirAll(files, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "data"), []byte("actual"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Set{
		Schema: SchemaVersion, ID: "set-1", App: "app", Target: "config", State: StateVerified, Dir: dir,
		Files: []File{{Path: "data", Size: 6, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("expected")))}},
	}
	if err := Verify(s); err == nil {
		t.Fatal("hash mismatch passed verification")
	}
}

func TestVerifyDetectsUnexpectedEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "set-1")
	files := filepath.Join(dir, "files")
	if err := os.MkdirAll(files, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "unexpected"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Set{Schema: SchemaVersion, ID: "set-1", App: filepath.Base(filepath.Dir(dir)), Target: "config", Dir: dir}
	if err := Verify(s); err == nil {
		t.Fatal("unexpected backup entry passed verification")
	}
}

func TestCreatingBackupCannotBeDeleted(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "BACKUP.json")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	set := &Set{ID: "set-1", App: "app", Target: "config", State: StateCreating, Dir: dir}
	err := NewManager(t.TempDir()).Delete(set, DeleteOptions{SkipConfirm: true})
	if err == nil {
		t.Fatal("creating backup was deleted")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("protected backup changed: %q, %v", got, err)
	}
}
