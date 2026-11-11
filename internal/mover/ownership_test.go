package mover

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/platform"
)

func TestOwnedCleanupRequiresMatchingOperation(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "state.midas-partial")
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkOwnedPath(root, partial, id, "move-partial"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(partial, "keep.txt")
	if err := os.WriteFile(data, []byte("recoverable"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherID, _ := NewOperationID()
	if err := RemoveOwnedPath(root, partial, otherID, "move-partial"); err == nil {
		t.Fatal("mismatched operation ID authorized cleanup")
	}
	if got, err := os.ReadFile(data); err != nil || string(got) != "recoverable" {
		t.Fatalf("partial data changed: %q, %v", got, err)
	}
	if err := RemoveOwnedPath(root, partial, id, "move-partial"); err != nil {
		t.Fatal(err)
	}
	if PathExists(partial) || PathExists(ownerPath(partial)) {
		t.Fatal("owned partial and marker were not removed")
	}
}

func TestUnknownAndCorruptPartialsArePreserved(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "unrelated.midas-partial")
	if err := os.Mkdir(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(partial, "user-data")
	if err := os.WriteFile(data, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, _ := NewOperationID()
	if err := RemoveOwnedPath(root, partial, id, "move-partial"); err == nil {
		t.Fatal("unowned partial was removed")
	}
	if err := os.WriteFile(ownerPath(partial), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOwnedPath(root, partial, id, "move-partial"); err == nil {
		t.Fatal("corrupt ownership metadata authorized cleanup")
	}
	if got, err := os.ReadFile(data); err != nil || string(got) != "keep" {
		t.Fatalf("unknown partial data changed: %q, %v", got, err)
	}
}

func TestOwnedCleanupDoesNotFollowDirectoryLink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	keep := filepath.Join(external, "keep")
	if err := os.WriteFile(keep, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(root, "linked.midas-partial")
	id, _ := NewOperationID()
	if err := MarkOwnedPath(root, partial, id, "move-partial"); err != nil {
		t.Fatal(err)
	}
	if err := platform.Current().CreateLink(partial, external); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	defer platform.Current().RemoveLink(partial)
	if err := RemoveOwnedPath(root, partial, id, "move-partial"); err == nil {
		t.Fatal("directory link was treated as an owned directory")
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "outside" {
		t.Fatalf("external target changed: %q, %v", got, err)
	}
}

func TestJournalIdentityAndCorruptScan(t *testing.T) {
	vault := t.TempDir()
	if got := JournalPath(vault, "../escape", "target"); got != "" {
		t.Fatalf("traversal journal path accepted: %q", got)
	}
	source := filepath.Join(t.TempDir(), "source")
	dest := filepath.Join(vault, "apps", "example", "config")
	j := &Journal{
		Operation: "move", App: "example", Target: "config", State: StateCopying,
		Source: source, Dest: dest, Partial: dest + ".midas-partial", Bak: source + ".midas-bak", NoBackup: true,
	}
	if err := SaveJournal(vault, j); err != nil {
		t.Fatal(err)
	}
	if j.OperationID == "" {
		t.Fatal("journal did not get an operation ID")
	}
	if err := os.WriteFile(filepath.Join(vault, ".midas", "journal", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid, issues, err := ScanJournals(vault)
	if err != nil || len(valid) != 1 || len(issues) != 1 {
		t.Fatalf("scan = %d valid, %d issues, %v", len(valid), len(issues), err)
	}
}

func TestTreeSnapshotDetectsSameSizeMutation(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "state")
	if err := os.WriteFile(file, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := TreeSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(file, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	after, err := TreeSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if SameTreeSnapshot(before, after) {
		t.Fatal("same-size source mutation was not detected")
	}
}

func TestCopyPreservesRelativeDirectoryLinkWithinTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relative directory symlinks are not supported as Windows junctions")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	dest := filepath.Join(root, "destination")
	if err := os.MkdirAll(filepath.Join(source, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "target", "value"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(source, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyTree(source, dest, CopyOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTree(source, dest, true); err != nil {
		a, _, _ := platform.Current().ReadLink(filepath.Join(source, "alias"))
		b, _, _ := platform.Current().ReadLink(filepath.Join(dest, "alias"))
		t.Fatalf("VerifyTree failed for source link %q and destination link %q: %v", a, b, err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "alias", "value")); err != nil || string(got) != "data" {
		t.Fatalf("copied relative link did not resolve within destination: %q, %v", got, err)
	}
}

func TestCopyCancellationLeavesPartialForRecovery(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(root, "destination.midas-partial")
	if _, err := CopyTree(source, dest, CopyOptions{Context: ctx}); err == nil {
		t.Fatal("canceled copy unexpectedly succeeded")
	}
	if !PathExists(dest) {
		t.Fatal("canceled copy did not preserve its partial directory")
	}
}

func TestMovePreservesUnknownPartialDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	partial := dest + ".midas-partial"
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "source"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(partial, "user-data")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"}, Source: source, Dest: dest, Vault: vault,
		NoBackupConfirmed: true, Manifest: manifest.Empty(),
	})
	if err == nil {
		t.Fatal("move unexpectedly accepted an unknown partial directory")
	}
	if got, readErr := os.ReadFile(keep); readErr != nil || string(got) != "keep" {
		t.Fatalf("unknown partial was modified: %q, %v", got, readErr)
	}
}

func TestMoveRejectsVaultAppSymlinkSubstitution(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	external := t.TempDir()
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "source"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(external, "user-data")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := platform.Current().CreateLink(filepath.Join(vault, "apps"), external); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	_, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"}, Source: source,
		Dest: filepath.Join(vault, "apps", "example", "data"), Vault: vault,
		NoBackupConfirmed: true, Manifest: manifest.Empty(),
	})
	if err == nil {
		t.Fatal("move accepted a vault app-directory substitution")
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "keep" {
		t.Fatalf("external app directory was modified: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "source")); err != nil || string(got) != "source" {
		t.Fatalf("source was modified: %q, %v", got, err)
	}
}

func TestResumeRestartsOnlyOwnedCopyCheckpoint(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	partial := dest + ".midas-partial"
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveJournal(vault, &Journal{
		OperationID: id, Operation: "move", App: "example", Target: "data", State: StateCopying,
		Source: source, Dest: dest, Partial: partial, Bak: source + ".midas-bak", NoBackup: true,
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
	_, err = MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"}, Resume: true, OperationID: id,
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: manifest.Empty(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("resumed link unhealthy: %s", detail)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "state")); err != nil || string(got) != "current" {
		t.Fatalf("resumed data mismatch: %q, %v", got, err)
	}
	if PathExists(filepath.Join(dest, "incomplete")) {
		t.Fatal("stale, operation-owned partial contents remained in committed destination")
	}
	if JournalExists(vault, "example", "data") {
		t.Fatal("completed resume left an operation journal")
	}
}

func TestRestoreAndRollbackPreserveMovedData(t *testing.T) {
	for _, recovery := range []string{"restore", "rollback"} {
		t.Run(recovery, func(t *testing.T) {
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
			if _, err := MoveTarget(MoveOptions{
				App: "example", Target: apps.Target{Name: "data"}, Source: source, Dest: dest, Vault: vault,
				NoBackupConfirmed: true, Manifest: m,
			}); err != nil {
				t.Fatal(err)
			}
			if recovery == "restore" {
				err := Restore(RestoreOptions{App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m, Verify: "hash"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := Rollback(RollbackOptions{App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m}); err != nil {
					t.Fatal(err)
				}
			}
			info, err := os.Lstat(source)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("source was not restored as a real directory: %v, %v", info, err)
			}
			if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
				t.Fatalf("restored data mismatch: %q, %v", got, err)
			}
			if JournalExists(vault, "example", "data") {
				t.Fatal("completed recovery left an operation journal")
			}
		})
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
