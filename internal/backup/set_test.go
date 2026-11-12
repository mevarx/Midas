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

func TestLoadSetRejectsUnknownMetadataFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app", "set")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":1,"id":"set","app":"app","target":"target","mode":"full","state":"verified","files":[],"skipped":[],"future_recovery_field":true}`)
	if err := os.WriteFile(filepath.Join(dir, "BACKUP.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSet(dir); err == nil {
		t.Fatal("unknown backup metadata field was silently discarded")
	}
}

func TestLoadLegacyBackupWithoutDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app", "set")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A set written before the additive dirs list existed: no "dirs" field.
	data := []byte(`{
  "schema": 1,
  "id": "set",
  "app": "app",
  "target": "target",
  "original_path": "/home/example",
  "mode": "full",
  "state": "verified",
  "created_at": "2024-01-01T00:00:00Z",
  "files": [
    {"path": "state", "size": 5, "mtime": "2024-01-01T00:00:00Z", "mode": 384}
  ],
  "skipped": [],
  "restore_point": {}
}`)
	if err := os.WriteFile(filepath.Join(dir, "BACKUP.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := LoadSet(dir)
	if err != nil {
		t.Fatalf("legacy BACKUP.json without dirs did not load: %v", err)
	}
	if len(set.Dirs) != 0 {
		t.Fatalf("legacy set gained directory records: %+v", set.Dirs)
	}
	if len(set.Files) != 1 || set.Files[0].Path != "state" {
		t.Fatalf("legacy set files = %+v", set.Files)
	}
}

func TestValidateDirectoryRecords(t *testing.T) {
	base := func(dirs []Dir, files []File) *Set {
		return &Set{
			Schema: SchemaVersion, ID: "set-1", App: "app", Target: "config",
			Dir: filepath.Join(t.TempDir(), "app", "set-1"), Files: files, Dirs: dirs,
		}
	}
	if err := base([]Dir{{Path: "."}}, nil).Validate(); err != nil {
		t.Fatalf("root directory record was rejected: %v", err)
	}
	if err := base([]Dir{{Path: ".", Mode: 0o700}, {Path: ".", Mode: 0o700}}, nil).Validate(); err == nil {
		t.Fatal("duplicate root directory record was accepted")
	}
	if err := base([]Dir{{Path: "../escape"}}, nil).Validate(); err == nil {
		t.Fatal("directory traversal was accepted")
	}
	if err := base([]Dir{{Path: "Dir"}, {Path: "dir"}}, nil).Validate(); err == nil {
		t.Fatal("case-folded duplicate directories were accepted")
	}
	if err := base([]Dir{{Path: "state"}}, []File{{Path: "state", Size: 1}}).Validate(); err == nil {
		t.Fatal("directory duplicating a file path was accepted")
	}
}
