package mover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/platform"
)

func TestRestoreReturnsDataAndClearsVaultCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, BackupSet: set, Manifest: m,
	}); err != nil {
		t.Fatal(err)
	}
	if err := Restore(RestoreOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, Verify: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	assertRealDirectory(t, source, "source after restore")
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("restored data mismatch: %q, %v", got, err)
	}
	if PathExists(dest) {
		t.Fatal("restore did not remove the vault copy")
	}
	if entries, err := os.ReadDir(filepath.Join(vault, "apps", "example")); err != nil || len(entries) != 0 {
		t.Fatalf("vault app directory not cleared: %v, %v", entries, err)
	}
	if idx, _ := m.Find("example", "data"); idx >= 0 {
		t.Fatal("restore left a manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
	if set.State != backup.StateComplete {
		t.Fatalf("restore changed the backup set state to %q", set.State)
	}
}

func TestRestoreKeepVaultCopyKeepsBothTrees(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	writeSourceFixture(t, source)
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: m,
	}); err != nil {
		t.Fatal(err)
	}
	if err := Restore(RestoreOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, Verify: "hash", KeepVaultCopy: true,
	}); err != nil {
		t.Fatal(err)
	}
	assertRealDirectory(t, source, "source after restore")
	if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
		t.Fatalf("restored data mismatch: %q, %v", got, err)
	}
	if !PathExists(dest) {
		t.Fatal("keep-vault-copy restore removed the vault copy")
	}
	if _, err := VerifyTree(source, dest, true); err != nil {
		t.Fatalf("restored and vault copies differ: %v", err)
	}
	if idx, _ := m.Find("example", "data"); idx >= 0 {
		t.Fatal("restore left a manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRestoreInterruptionPreservesEverythingAndRetries(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "home", "data")
	vault := filepath.Join(root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Large enough that the copy is interrupted after real progress.
	data := make([]byte, 8<<20)
	for i := range 4 {
		if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("%02d", i)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: m,
	}); err != nil {
		t.Fatal(err)
	}
	vaultSnapshot, err := TreeSnapshot(dest)
	if err != nil {
		t.Fatal(err)
	}
	// Interrupt the restore copy mid-flight through the progress reporter.
	ctx, cancel := context.WithCancel(context.Background())
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	err = Restore(RestoreOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, KeepVaultCopy: true, OperationID: id, Context: ctx,
		Progress: cancelOnProgress{cancel}, Verify: "hash",
	})
	cancel()
	if err == nil {
		t.Fatal("interrupted restore unexpectedly succeeded")
	}
	// Nothing was lost: the link still serves the data and the vault copy
	// is byte-identical to before the interruption.
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("link broken by interrupted restore: %s", detail)
	}
	after, err := TreeSnapshot(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !SameTreeSnapshot(vaultSnapshot, after) {
		t.Fatal("vault copy changed during interrupted restore")
	}
	// The partial copy is preserved and provably owned by the operation.
	partial := source + ".midas-partial"
	if !PathExists(partial) {
		t.Fatal("interrupted restore did not preserve its partial copy")
	}
	if err := ValidateOwnedPath(filepath.Dir(partial), partial, id, "restore-partial"); err != nil {
		t.Fatalf("interrupted restore left an unprovable partial: %v", err)
	}
	j, err := LoadJournal(vault, "example", "data")
	if err != nil || j == nil || j.State != StateRestoring {
		t.Fatalf("interrupted restore journal = %#v, %v", j, err)
	}
	// Recovery: clear the owned partial (as doctor does), then retry.
	if err := RemoveOwnedPath(filepath.Dir(partial), partial, id, "restore-partial"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveJournal(vault, "example", "data"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(RestoreOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, KeepVaultCopy: true, OperationID: id, Verify: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	assertRealDirectory(t, source, "source after retried restore")
	if got, err := os.ReadFile(filepath.Join(source, "03")); err != nil || len(got) != len(data) {
		t.Fatalf("retried restore data mismatch: %d bytes, %v", len(got), err)
	}
	if _, isLink, err := platform.Current().ReadLink(source); err != nil || isLink {
		t.Fatalf("source is still a link after restore: %t, %v", isLink, err)
	}
	if !PathExists(dest) {
		t.Fatal("keep-vault-copy retry removed the vault copy")
	}
	kept, err := TreeSnapshot(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !SameTreeSnapshot(vaultSnapshot, kept) {
		t.Fatal("kept vault copy changed during restore")
	}
	if idx, _ := m.Find("example", "data"); idx >= 0 {
		t.Fatal("retried restore left a manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRestoreRefusals(t *testing.T) {
	t.Run("missing vault copy", func(t *testing.T) {
		root := t.TempDir()
		vault := filepath.Join(root, "vault")
		source := filepath.Join(root, "home", "data")
		if err := os.MkdirAll(source, 0o700); err != nil {
			t.Fatal(err)
		}
		err := Restore(RestoreOptions{
			App: "example", Target: "data", Source: source,
			Dest: filepath.Join(vault, "apps", "example", "data"), Vault: vault, Manifest: manifest.Empty(),
		})
		if err == nil || !strings.Contains(err.Error(), "vault copy") {
			t.Fatalf("missing vault copy error = %v", err)
		}
	})
	t.Run("existing partial preserved", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "home", "data")
		vault := filepath.Join(root, "vault")
		dest := filepath.Join(vault, "apps", "example", "data")
		writeSourceFixture(t, source)
		m := manifest.Empty()
		if _, err := MoveTarget(MoveOptions{
			App: "example", Target: apps.Target{Name: "data"},
			Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: m,
		}); err != nil {
			t.Fatal(err)
		}
		partial := source + ".midas-partial"
		keep := filepath.Join(partial, "user-data")
		if err := os.MkdirAll(partial, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := Restore(RestoreOptions{
			App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m,
		})
		if err == nil || !strings.Contains(err.Error(), "partial path") {
			t.Fatalf("existing partial error = %v", err)
		}
		if got, err := os.ReadFile(keep); err != nil || string(got) != "keep" {
			t.Fatalf("foreign partial was modified: %q, %v", got, err)
		}
		if ok, detail := HealthCheck(source, dest); !ok {
			t.Fatalf("refused restore broke the link: %s", detail)
		}
	})
	t.Run("existing journal preserved", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "home", "data")
		vault := filepath.Join(root, "vault")
		dest := filepath.Join(vault, "apps", "example", "data")
		writeSourceFixture(t, source)
		m := manifest.Empty()
		if _, err := MoveTarget(MoveOptions{
			App: "example", Target: apps.Target{Name: "data"},
			Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: m,
		}); err != nil {
			t.Fatal(err)
		}
		// Fabricate a second operation journal, as an interrupted operation
		// would leave behind.
		id, err := NewOperationID()
		if err != nil {
			t.Fatal(err)
		}
		if err := SaveJournal(vault, &Journal{
			OperationID: id, Operation: "move", App: "example", Target: "data", State: StateCopying,
			Source: source, Dest: dest, Partial: dest + ".midas-partial", Bak: source + ".midas-bak",
		}); err != nil {
			t.Fatal(err)
		}
		err = Restore(RestoreOptions{
			App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m,
		})
		if err == nil || !strings.Contains(err.Error(), "journal already exists") {
			t.Fatalf("existing journal error = %v", err)
		}
		if ok, detail := HealthCheck(source, dest); !ok {
			t.Fatalf("refused restore broke the link: %s", detail)
		}
		assertNoJournalChange(t, vault, "example", "data", id)
	})
}

// assertNoJournalChange asserts the fabricated journal is still present with
// its original identity after a refused operation.
func assertNoJournalChange(t *testing.T, vault, app, target, id string) {
	t.Helper()
	j, err := LoadJournal(vault, app, target)
	if err != nil || j == nil || j.OperationID != id {
		t.Fatalf("refused restore disturbed the journal: %#v, %v", j, err)
	}
}
