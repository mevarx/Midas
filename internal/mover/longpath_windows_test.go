//go:build windows

package mover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
)

// longPath builds a directory chain under base whose full path exceeds the
// traditional 260-character MAX_PATH boundary, as docs/DECISIONS.md requires
// verifying for Windows support.
func longPath(t *testing.T, base, name string) string {
	t.Helper()
	path := filepath.Join(base, name)
	for len(path) <= 260 {
		next := filepath.Join(path, strings.Repeat("segment", 4))
		if err := os.MkdirAll(next, 0o700); err != nil {
			t.Fatal(err)
		}
		path = next
	}
	return path
}

func TestMoveRestoreRollbackBeyondMaxPath(t *testing.T) {
	root := t.TempDir()
	source := longPath(t, root, "source")
	vault := longPath(t, root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	if len(source) <= 260 || len(dest) <= 260 {
		t.Fatalf("fixture paths must exceed 260 characters: source=%d dest=%d", len(source), len(dest))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "extra"), []byte("more"), 0o600); err != nil {
		t.Fatal(err)
	}
	set := newVerifiedBackupSet(t, source, backup.ModeFull, apps.BackupRule{})
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, BackupSet: set, Manifest: m,
	}); err != nil {
		t.Fatalf("move with paths beyond MAX_PATH: %v", err)
	}
	if ok, detail := HealthCheck(source, dest); !ok {
		t.Fatalf("junction beyond MAX_PATH is unhealthy: %s", detail)
	}
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("data unreachable through long junction: %q, %v", got, err)
	}
	if err := Restore(RestoreOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, Verify: "hash",
	}); err != nil {
		t.Fatalf("restore with paths beyond MAX_PATH: %v", err)
	}
	assertRealDirectory(t, source, "source after long-path restore")
	if got, err := os.ReadFile(filepath.Join(source, "nested", "extra")); err != nil || string(got) != "more" {
		t.Fatalf("restored long-path data mismatch: %q, %v", got, err)
	}
	assertNoJournal(t, vault, "example", "data")
}

func TestRollbackBeyondMaxPath(t *testing.T) {
	root := t.TempDir()
	source := longPath(t, root, "source")
	vault := longPath(t, root, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := manifest.Empty()
	if _, err := MoveTarget(MoveOptions{
		App: "example", Target: apps.Target{Name: "data"},
		Source: source, Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: m,
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := Rollback(RollbackOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault, Manifest: m,
	})
	if err != nil {
		t.Fatalf("rollback with paths beyond MAX_PATH: %v", err)
	}
	if !rep.Restored {
		t.Fatal("long-path rollback restored nothing")
	}
	assertRealDirectory(t, source, "source after long-path rollback")
	if got, err := os.ReadFile(filepath.Join(source, "state")); err != nil || string(got) != "value" {
		t.Fatalf("long-path rollback data mismatch: %q, %v", got, err)
	}
	assertNoJournal(t, vault, "example", "data")
}
