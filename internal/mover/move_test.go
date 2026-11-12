package mover

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
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

func TestCreateJournalRefusesToOverwrite(t *testing.T) {
	vault := t.TempDir()
	source := filepath.Join(t.TempDir(), "home", "data")
	dest := filepath.Join(vault, "apps", "example", "data")
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	j := &Journal{
		OperationID: id, Operation: "move", App: "example", Target: "data", State: StateCopying,
		Source: source, Dest: dest, Partial: dest + ".midas-partial", Bak: source + ".midas-bak",
	}
	if err := CreateJournal(vault, j); err != nil {
		t.Fatalf("first CreateJournal: %v", err)
	}
	otherID, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	err = CreateJournal(vault, &Journal{
		OperationID: otherID, Operation: "move", App: "example", Target: "data", State: StateCopying,
		Source: source, Dest: dest, Partial: dest + ".midas-partial", Bak: source + ".midas-bak",
	})
	if err == nil {
		t.Fatal("second CreateJournal overwrote an existing journal")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("second CreateJournal error = %v, want os.ErrExist", err)
	}
	if !strings.Contains(err.Error(), "example/data") {
		t.Fatalf("second CreateJournal error does not name the app/target: %v", err)
	}
	loaded, err := LoadJournal(vault, "example", "data")
	if err != nil || loaded == nil || loaded.OperationID != id {
		t.Fatalf("journal after refused create = %+v, %v; want operation %s", loaded, err, id)
	}
	loaded.State = StateVerifying
	if err := SaveJournal(vault, loaded); err != nil {
		t.Fatalf("SaveJournal update: %v", err)
	}
	updated, err := LoadJournal(vault, "example", "data")
	if err != nil || updated == nil || updated.State != StateVerifying {
		t.Fatalf("updated journal = %+v, %v; want state %s", updated, err, StateVerifying)
	}
}

// forceCopyPath makes MoveTarget take the cross-volume copy path, which is
// the only path that stages a .midas-bak for step 11 to delete.
func forceCopyPath(t *testing.T) {
	t.Helper()
	orig := sameVolumeCheck
	sameVolumeCheck = func(string, string) (bool, error) { return false, nil }
	t.Cleanup(func() { sameVolumeCheck = orig })
}

func TestMoveAbortsWhenAppStartsBeforeBakRemoval(t *testing.T) {
	forceCopyPath(t)
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
	bak := source + ".midas-bak"

	// First call runs before the original is swapped aside, so .midas-bak
	// must not exist yet. The second call runs before step 11 removes it and
	// reports the app as running.
	calls := 0
	_, err = MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, Verify: "size",
		SourceBytes: sourceBytes, SourceFiles: sourceFiles,
		BackupSet: set, Manifest: manifest.Empty(),
		Recheck: func() error {
			calls++
			switch calls {
			case 1:
				if PathExists(bak) {
					t.Errorf("first Recheck ran after .midas-bak was created")
				}
				return nil
			case 2:
				if !PathExists(bak) {
					t.Errorf("second Recheck ran without the .midas-bak it guards")
				}
				return errors.New("profile is in use")
			default:
				t.Errorf("Recheck called %d times, want exactly 2", calls)
				return nil
			}
		},
	})
	if err == nil {
		t.Fatal("move succeeded although the app started running")
	}
	if !strings.Contains(err.Error(), "app started running") {
		t.Fatalf("error = %v, want it to mention the app running", err)
	}
	if calls != 2 {
		t.Fatalf("Recheck calls = %d, want 2 (before the swap and before .midas-bak removal)", calls)
	}
	// The automatic rollback must have restored the untouched original and
	// removed the staging copy. The vault copy is intentionally kept.
	assertRealDirectory(t, source, "original after rollback")
	if got, rerr := os.ReadFile(filepath.Join(source, "state")); rerr != nil || string(got) != "value" {
		t.Fatalf("original state file after rollback = %q, %v; want %q", got, rerr, "value")
	}
	if got, rerr := os.ReadFile(filepath.Join(source, "nested", "extra")); rerr != nil || string(got) != "more" {
		t.Fatalf("original nested file after rollback = %q, %v; want %q", got, rerr, "more")
	}
	if PathExists(bak) {
		t.Fatalf(".midas-bak %q still exists after rollback", bak)
	}
	if _, isLink, lerr := platform.Current().ReadLink(source); lerr != nil || isLink {
		t.Fatalf("original path after rollback = link? %t, %v; want a real directory", isLink, lerr)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestMoveRecheckRunsTwiceOnForcedCopyPath(t *testing.T) {
	forceCopyPath(t)
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
	calls := 0
	_, err = MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, Verify: "size",
		SourceBytes: sourceBytes, SourceFiles: sourceFiles,
		BackupSet: set, Manifest: m,
		Recheck: func() error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The forced cross-volume path swaps the original into .midas-bak before
	// step 11, so it must recheck once before the swap and once before the
	// deletion: two successful calls. A same-volume move would see one call,
	// because no .midas-bak is ever created.
	if calls != 2 {
		t.Fatalf("Recheck calls = %d, want 2 on the forced copy path", calls)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("forced copy move left an unhealthy link: %s", detail)
	}
	if got, rerr := os.ReadFile(filepath.Join(source, "state")); rerr != nil || string(got) != "value" {
		t.Fatalf("data is not reachable through the link: %q, %v", got, rerr)
	}
	if PathExists(source + ".midas-bak") {
		t.Fatal("forced copy move left a .midas-bak behind")
	}
	assertNoJournal(t, vault, "example", "data")
}
