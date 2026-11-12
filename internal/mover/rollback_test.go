package mover

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
)

// completeMoveWithBackup performs a real move and returns the finalized state
// that rollback starts from.
func completeMoveWithBackup(t *testing.T, root, source, vault, dest string) (*backup.Set, *manifest.Manifest) {
	t.Helper()
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, BackupSet: set, Manifest: m,
	}); err != nil {
		t.Fatal(err)
	}
	return set, m
}

func TestRollbackFromBakRestoresOriginalAndKeepsCopies(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	set, m := completeMoveWithBackup(t, root, source, vault, dest)
	// Fabricate the untouched original that a cross-volume move keeps until
	// cleanup: the vault copy is a verified copy of the same tree.
	if _, err := CopyTree(dest, source+".midas-bak", CopyOptions{}); err != nil {
		t.Fatal(err)
	}
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		BackupSet: set, Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restored || !strings.Contains(rep.SourceUsed, ".midas-bak") {
		t.Fatalf("rollback report = restored=%t source=%q, want restored from .midas-bak", rep.Restored, rep.SourceUsed)
	}
	assertRealDirectory(t, source, "source after rollback")
	if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
		t.Fatalf("rolled back data mismatch: %q, %v", got, err)
	}
	if PathExists(source + ".midas-bak") {
		t.Fatal("rollback left the .midas-bak in place")
	}
	if _, isLink, err := platform.Current().ReadLink(source); err != nil || isLink {
		t.Fatalf("rollback did not remove the link: %t, %v", isLink, err)
	}
	// Rollback never deletes the vault copy or the backup set.
	if !PathExists(dest) {
		t.Fatal("rollback deleted the vault copy")
	}
	if _, err := VerifyTree(source, dest, true); err != nil {
		t.Fatalf("vault copy no longer matches the restored original: %v", err)
	}
	if set.State != backup.StateRolledBack {
		t.Fatalf("backup set state = %q, want rolled-back", set.State)
	}
	if idx, _ := m.Find("example", "data"); idx >= 0 {
		t.Fatal("rollback left a manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackFromVaultCopyWhenNoBak(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	_, m := completeMoveWithBackup(t, root, source, vault, dest)
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restored || !strings.Contains(rep.SourceUsed, "vault copy") {
		t.Fatalf("rollback report = restored=%t source=%q, want the vault copy", rep.Restored, rep.SourceUsed)
	}
	assertRealDirectory(t, source, "source after rollback")
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("rolled back data mismatch: %q, %v", got, err)
	}
	if PathExists(dest) {
		t.Fatal("vault copy was not moved back to the original path")
	}
	if idx, _ := m.Find("example", "data"); idx >= 0 {
		t.Fatal("rollback left a manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackFromBackupSetVerifiesRestoredData(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	m := manifest.Empty()
	if err := set.MarkInUse(); err != nil {
		t.Fatal(err)
	}
	// Everything is gone: no original, no .midas-bak, no vault copy.
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		BackupSet: set, Manifest: m, Prefer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restored || !strings.Contains(rep.SourceUsed, set.ID) {
		t.Fatalf("rollback report = restored=%t source=%q, want the backup set", rep.Restored, rep.SourceUsed)
	}
	assertRealDirectory(t, source, "source after backup restore")
	if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
		t.Fatalf("backup-restored data mismatch: %q, %v", got, err)
	}
	// The protected set tree is read-only; the restored data must carry the
	// recorded original modes instead.
	for _, f := range set.Files {
		if f.Link != "" {
			continue
		}
		fi, err := os.Lstat(filepath.Join(source, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != os.FileMode(f.Mode) {
			t.Fatalf("restored %q mode = %v, want recorded %v", f.Path, fi.Mode().Perm(), os.FileMode(f.Mode))
		}
	}
	if set.State != backup.StateRolledBack {
		t.Fatalf("backup set state = %q, want rolled-back", set.State)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackFromEssentialBackupListsNotCovered(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeEssential, apps.BackupRule{
		Exclude: []string{"state"},
	})
	m := manifest.Empty()
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		BackupSet: set, Manifest: m, Prefer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.NotCovered) == 0 {
		t.Fatalf("essential rollback reported no uncovered files: %+v", rep)
	}
	covered := strings.Join(rep.NotCovered, "\n")
	if !strings.Contains(covered, "state") {
		t.Fatalf("excluded file not reported as uncovered: %v", rep.NotCovered)
	}
	if PathExists(filepath.Join(source, "state")) {
		t.Fatal("file excluded from the backup was restored")
	}
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("covered file was not restored: %q, %v", got, err)
	}
	if set.State != backup.StateRolledBack {
		t.Fatalf("backup set state = %q, want rolled-back", set.State)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackRefusesCorruptBackupSet(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	if err := set.MarkInUse(); err != nil {
		t.Fatal(err)
	}
	// Simulate total loss of the live data: the backup set is the only copy.
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	// Failure injection: the only recovery source is invalid, so rollback
	// must refuse rather than partially restore.
	bad := filepath.Join(set.FilesDir(), "state")
	if err := os.Chmod(bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		BackupSet: set, Manifest: manifest.Empty(), Prefer: "backup",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid backup set") {
		t.Fatalf("corrupt backup set rollback error = %v", err)
	}
	if PathExists(source) {
		t.Fatal("refused rollback created data at the original path")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackDryRunChangesNothing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	_, m := completeMoveWithBackup(t, root, source, vault, dest)
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmed := false
	for _, note := range rep.Notes {
		if strings.Contains(note, "dry run: nothing was changed") {
			confirmed = true
		}
	}
	if !confirmed {
		t.Fatalf("dry run notes missing confirmation: %v", rep.Notes)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("dry run disturbed the link: %s", detail)
	}
	if PathExists(source + ".midas-bak") {
		t.Fatal("dry run created a .midas-bak")
	}
	if idx, _ := m.Find("example", "data"); idx < 0 {
		t.Fatal("dry run removed the manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackRefusesUnknownRollbackSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	_, m := completeMoveWithBackup(t, root, source, vault, dest)
	_, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, Prefer: "disk",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown rollback source") {
		t.Fatalf("unknown source error = %v", err)
	}
	_, err = Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, Prefer: "backup",
	})
	if err == nil || !strings.Contains(err.Error(), "no usable backup set") {
		t.Fatalf("missing backup source error = %v", err)
	}
}

func TestRollbackJournaledMismatchIsRefused(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	_, m := completeMoveWithBackup(t, root, source, vault, dest)
	// A journal whose paths do not match the requested rollback must stop it.
	otherSource := filepath.Join(root, "home", "other")
	if err := os.MkdirAll(otherSource, 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveJournal(vault, &Journal{
		OperationID: id, Operation: "move", App: "example", Target: "data", State: StateCopying,
		Source: otherSource, Dest: dest, Partial: dest + ".midas-partial", Bak: otherSource + ".midas-bak",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m,
	})
	if err == nil || !strings.Contains(err.Error(), "journal paths do not match") {
		t.Fatalf("mismatched journal rollback error = %v", err)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("refused rollback broke the link: %s", detail)
	}
	if got, err := LoadJournal(vault, "example", "data"); err != nil || got == nil || got.OperationID != id {
		t.Fatalf("refused rollback disturbed the journal: %#v, %v", got, err)
	}
}

func TestCanceledRollbackKeepsJournalAndCanRetry(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{App: "example", Target: apps.Target{Name: "data"}, Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: m}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Rollback(RollbackOptions{App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m, Context: ctx}); err == nil {
		t.Fatal("canceled rollback unexpectedly succeeded")
	}
	j, err := LoadJournal(vault, "example", "data")
	if err != nil || j == nil || j.State != StateNeedsRecovery {
		t.Fatalf("canceled rollback journal = %#v, %v", j, err)
	}
	if _, err := Rollback(RollbackOptions{App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
		t.Fatalf("retry did not restore data: %q, %v", got, err)
	}
	if JournalExists(vault, "example", "data") {
		t.Fatal("successful retry left a journal")
	}
}

// TestRollbackRestoresDirectoryModesAndTimes covers the full round trip for
// directory metadata: recorded modes and mtimes (including the tree root and an
// empty directory that only exists as a directory) are restored.
func TestRollbackRestoresDirectoryModesAndTimes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	t.Cleanup(func() { unlockProtectedTree(source) })

	open := filepath.Join(source, "open")
	locked := filepath.Join(source, "locked")
	empty := filepath.Join(source, "empty")
	for _, dir := range []string{open, locked, empty} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(open, "a"), []byte("a-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "b"), []byte("b-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		rel  string
		mode os.FileMode
	}{
		{".", 0o750},
		{"open", 0o755},
		{"locked", 0o555},
		{"empty", 0o700},
	}
	past := time.Now().Add(-2 * time.Second)
	for _, w := range want {
		p := source
		if w.rel != "." {
			p = filepath.Join(source, filepath.FromSlash(w.rel))
		}
		if err := os.Chmod(p, w.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	sourceTimes := make(map[string]time.Time, len(want))
	sourceModes := make(map[string]os.FileMode, len(want))
	for _, w := range want {
		p := source
		if w.rel != "." {
			p = filepath.Join(source, filepath.FromSlash(w.rel))
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		sourceTimes[w.rel] = fi.ModTime()
		sourceModes[w.rel] = fi.Mode().Perm()
		// Windows reports only a read-only bit, so exact POSIX modes are only
		// meaningful elsewhere; the restore must match what was recorded.
		if runtime.GOOS != "windows" && fi.Mode().Perm() != w.mode {
			t.Fatalf("source directory %q mode = %v, want %v", w.rel, fi.Mode().Perm(), w.mode)
		}
	}

	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	if len(set.Dirs) != len(want) {
		t.Fatalf("backup set recorded %d directories, want %d: %+v", len(set.Dirs), len(want), set.Dirs)
	}

	// Total loss of the live tree; the 0555 directory must be unlocked first so
	// its contents can be removed.
	if err := os.Chmod(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}

	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		BackupSet: set, Manifest: manifest.Empty(), Prefer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restored {
		t.Fatalf("rollback report = %+v, want a restore", rep)
	}
	for _, note := range rep.Notes {
		if strings.Contains(note, "predates directory metadata") {
			t.Fatalf("modern backup set produced the legacy metadata note: %v", rep.Notes)
		}
	}
	for _, w := range want {
		p := source
		if w.rel != "." {
			p = filepath.Join(source, filepath.FromSlash(w.rel))
		}
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("directory %q was not restored: %v", w.rel, err)
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%q is not a real directory: %v", w.rel, fi.Mode())
		}
		if fi.Mode().Perm() != sourceModes[w.rel] {
			t.Fatalf("directory %q mode = %v, want recorded %v", w.rel, fi.Mode().Perm(), sourceModes[w.rel])
		}
	}
	// Directory mtimes are compared with the recorded source times: copying
	// may add sub-second rounding, so allow a 2s window.
	for _, d := range set.Dirs {
		p := source
		if d.Path != "." {
			p = filepath.Join(source, filepath.FromSlash(d.Path))
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat restored directory %q: %v", d.Path, err)
		}
		wantTime, ok := sourceTimes[d.Path]
		if !ok {
			t.Fatalf("unexpected recorded directory %q", d.Path)
		}
		if diff := fi.ModTime().Sub(wantTime); diff > 2*time.Second || diff < -2*time.Second {
			t.Fatalf("directory %q mtime = %v, want within 2s of %v", d.Path, fi.ModTime(), wantTime)
		}
		if diff := fi.ModTime().Sub(d.MTime); diff > 2*time.Second || diff < -2*time.Second {
			t.Fatalf("directory %q mtime = %v, want within 2s of recorded %v", d.Path, fi.ModTime(), d.MTime)
		}
	}
	if got, err := os.ReadFile(filepath.Join(source, "open", "a")); err != nil || string(got) != "a-data" {
		t.Fatalf("restored file in a 0755 directory: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "locked", "b")); err != nil || string(got) != "b-data" {
		t.Fatalf("restored file in a 0555 directory: %q, %v", got, err)
	}
}

// TestRollbackLegacyBackupNotesMissingDirectoryMetadata covers sets saved
// without the additive dirs list: they still restore and the report says that
// directory modes and timestamps were not recorded.
func TestRollbackLegacyBackupNotesMissingDirectoryMetadata(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	set.Dirs = nil
	if err := set.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		BackupSet: set, Manifest: manifest.Empty(), Prefer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restored {
		t.Fatalf("rollback report = %+v, want a restore", rep)
	}
	noted := false
	for _, note := range rep.Notes {
		if strings.Contains(note, "predates directory metadata") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("legacy backup restore did not note missing directory metadata: %v", rep.Notes)
	}
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("legacy backup did not restore files: %q, %v", got, err)
	}
}
