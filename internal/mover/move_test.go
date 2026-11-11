package mover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/platform"
)

// newVerifiedBackupSet creates a fully verified backup set, exactly as the
// CLI does before every move, and arranges cleanup for its read-only tree.
func newVerifiedBackupSet(t *testing.T, source, mode string, rule apps.BackupRule) *backup.Set {
	t.Helper()
	set, err := backup.NewManager(t.TempDir()).Create(backup.CreateOptions{
		App:          "example",
		Target:       "data",
		OriginalPath: source,
		Mode:         mode,
		Rule:         rule,
	}, manifest.RestorePoint{})
	if err != nil {
		t.Fatalf("create backup set: %v", err)
	}
	t.Cleanup(func() { unlockProtectedTree(set.Dir) })
	return set
}

// unlockProtectedTree clears the read-only protection backup.Create applies
// so test cleanup can delete the tree.
func unlockProtectedTree(root string) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700)
		} else {
			_ = os.Chmod(p, 0o600)
		}
		return nil
	})
}

func writeSourceFixture(t *testing.T, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "extra"), []byte("more"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertRealDirectory(t *testing.T, path, what string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is not a real directory: %v, %v", what, info, err)
	}
}

func assertNoJournal(t *testing.T, vault, app, target string) {
	t.Helper()
	if JournalExists(vault, app, target) {
		t.Fatalf("operation left a journal for %s/%s", app, target)
	}
}

func TestMoveEndToEndWithVerifiedBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	sourceBytes, sourceFiles, err := Measure(source)
	if err != nil {
		t.Fatal(err)
	}
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	m := manifest.Empty()
	entry, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, Verify: "size",
		SourceBytes: sourceBytes, SourceFiles: sourceFiles,
		BackupSet: set, Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("move left an unhealthy link: %s", detail)
	}
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("data is not reachable through the link: %q, %v", got, err)
	}
	if _, isLink, err := platform.Current().ReadLink(source); err != nil || !isLink {
		t.Fatalf("original path = link? %t, %v", isLink, err)
	}
	idx, stored := m.Find("example", "data")
	if idx < 0 || stored == nil {
		t.Fatal("manifest has no entry for the moved target")
	}
	if stored.VaultPath != dest || stored.OriginalPath != source {
		t.Fatalf("manifest entry paths = %q -> %q, want %q -> %q", stored.OriginalPath, stored.VaultPath, source, dest)
	}
	if stored.OperationID == "" || stored.LinkType != platform.LinkType() {
		t.Fatalf("manifest entry identity incomplete: %+v", stored)
	}
	if stored.Bytes != sourceBytes || stored.Files != sourceFiles {
		t.Fatalf("manifest entry = %d bytes/%d files, want %d/%d", stored.Bytes, stored.Files, sourceBytes, sourceFiles)
	}
	if entry.Backup.ID != set.ID || entry.Backup.State != backup.StateComplete {
		t.Fatalf("returned backup info = %+v, want id %s complete", entry.Backup, set.ID)
	}
	if set.State != backup.StateComplete {
		t.Fatalf("backup set state = %q, want complete", set.State)
	}
	if _, err := os.Stat(set.JSONPath()); err != nil {
		t.Fatalf("backup set metadata missing after move: %v", err)
	}
	assertNoJournal(t, vault, "example", "data")
	if PathExists(source + ".midas-bak") {
		t.Fatal("same-volume move left a .midas-bak behind")
	}
}

func TestMoveRefusesWithoutBackupOrConfirmation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, Manifest: manifest.Empty(),
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to move without a verified backup set") {
		t.Fatalf("unconfirmed no-backup move error = %v", err)
	}
	assertRealDirectory(t, source, "source after refusal")
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveRefusesUnverifiedBackupSet(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	set.State = backup.StateCreating
	if err := set.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, BackupSet: set, Manifest: manifest.Empty(),
	})
	if err == nil || !strings.Contains(err.Error(), "creating, not verified") {
		t.Fatalf("unverified backup set error = %v", err)
	}
	assertRealDirectory(t, source, "source after refusal")
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveRefusesCorruptBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	// Failure injection: tamper with one protected backup file after
	// verification so the pre-move re-check must reject the set.
	bad := filepath.Join(set.FilesDir(), "state")
	if err := os.Chmod(bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, BackupSet: set, Manifest: manifest.Empty(),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid safety backup") {
		t.Fatalf("corrupt backup set error = %v", err)
	}
	assertRealDirectory(t, source, "source after refusal")
	assertNoJournal(t, vault, "example", "data")
	if PathExists(dest) || PathExists(dest+".midas-partial") {
		t.Fatal("refused move staged data in the vault")
	}
}

func TestMoveRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "occupied"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: manifest.Empty(),
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("occupied destination error = %v", err)
	}
	assertRealDirectory(t, source, "source after refusal")
	if got, err := os.ReadFile(filepath.Join(dest, "occupied")); err != nil || string(got) != "keep" {
		t.Fatalf("existing vault copy was modified: %q, %v", got, err)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveRefusesStaleOriginalBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	if err := os.MkdirAll(source+".midas-bak", 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: manifest.Empty(),
	})
	if err == nil || !strings.Contains(err.Error(), "doctor") {
		t.Fatalf("stale .midas-bak error = %v", err)
	}
	assertRealDirectory(t, source, "source after refusal")
	if PathExists(dest) {
		t.Fatal("refused move created a vault copy")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveAutoRollbackRecoversWhenManifestCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	// Failure injection: the manifest path is occupied by a directory, so the
	// manifest write fails only after the link was created and health-checked.
	if err := os.MkdirAll(filepath.Join(vault, ".midas", "manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: manifest.Empty(),
	})
	if err == nil {
		t.Fatal("move with an unwritable manifest unexpectedly succeeded")
	}
	// The automatic rollback must have fully undone the committed move.
	assertRealDirectory(t, source, "source after automatic rollback")
	if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
		t.Fatalf("original data was not restored: %q, %v", got, err)
	}
	if PathExists(dest) {
		t.Fatal("vault copy survived automatic rollback")
	}
	if entries, err := os.ReadDir(filepath.Join(vault, "apps", "example")); err != nil || len(entries) != 0 {
		t.Fatalf("vault app directory not emptied by rollback: %v, %v", entries, err)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveResumeWithBackupCompletesAndFinalizesBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	sourceBytes, sourceFiles, err := Measure(source)
	if err != nil {
		t.Fatal(err)
	}
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	// Simulate a crash after the copy stage: journal and owned partial remain.
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	partial := dest + ".midas-partial"
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveJournal(vault, &Journal{
		OperationID: id, Operation: "move", App: "example", Target: "data", State: StateCopying,
		Source: source, Dest: dest, Partial: partial, Bak: source + ".midas-bak", BackupID: set.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := MarkOwnedPath(filepath.Dir(partial), partial, id, "move-partial"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "incomplete"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := manifest.Empty()
	entry, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"}, Resume: true, OperationID: id,
		Source: source, Dest: dest, Vault: vault, Verify: "size",
		SourceBytes: sourceBytes, SourceFiles: sourceFiles,
		BackupSet: set, Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("resumed move left an unhealthy link: %s", detail)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "state")); err != nil || string(got) != "value" {
		t.Fatalf("resumed data mismatch: %q, %v", got, err)
	}
	if PathExists(filepath.Join(dest, "incomplete")) {
		t.Fatal("stale owned partial contents reached the destination")
	}
	if entry.Backup.ID != set.ID || entry.Backup.State != backup.StateComplete {
		t.Fatalf("resumed backup info = %+v, want id %s complete", entry.Backup, set.ID)
	}
	if set.State != backup.StateComplete {
		t.Fatalf("backup set state after resume = %q, want complete", set.State)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveResumeRejectsMismatchedJournal(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SaveJournal(vault, &Journal{
		OperationID: id, Operation: "move", App: "example", Target: "data", State: StateVerifying,
		Source: source, Dest: dest, Partial: dest + ".midas-partial", Bak: source + ".midas-bak",
		// A valid-format backup ID that belongs to a different set.
		BackupID: "19991231-000000",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"}, Resume: true,
		Source: source, Dest: dest, Vault: vault, BackupSet: set, Manifest: manifest.Empty(),
	})
	if err == nil || !strings.Contains(err.Error(), "not eligible for safe resume") {
		t.Fatalf("mismatched backup resume error = %v", err)
	}
	assertRealDirectory(t, source, "source after refused resume")
}
