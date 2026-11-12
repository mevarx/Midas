//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mevarx/Midas/internal/config"
	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/ui"
)

// snapshotTree records every path below root so tests can prove a command
// changed nothing in the vault.
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel+"|"+info.Mode().String())
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	sort.Strings(paths)
	return paths
}

func TestReadOnlyVaultDiagnostics(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not block writes")
	}
	vault := t.TempDir()
	if err := os.Mkdir(filepath.Join(vault, ".midas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(vault, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(vault, 0o755) })

	newCtx := func(out, errOut *bytes.Buffer) *Ctx {
		return &Ctx{
			G:   Global{Vault: vault},
			Cfg: &config.Config{Vault: vault, Backup: "none", Verify: "size"},
			UI:  &ui.UI{Out: out, Err: errOut},
		}
	}

	var stdout, stderr bytes.Buffer
	if err := newCtx(&stdout, &stderr).cmdStatus([]string{"--json"}); err != nil {
		t.Fatalf("status on a read-only vault failed: %v (stderr: %s)", err, stderr.String())
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("status did not emit JSON on a read-only vault: %v\n%s", err, stdout.String())
	}
	if report["entries"] == nil {
		t.Fatalf("status report is missing entries: %v", report)
	}

	before := snapshotTree(t, vault)
	var moveOut, moveErrOut bytes.Buffer
	moveErr := newCtx(&moveOut, &moveErrOut).cmdMove([]string{"claude-code"})
	if moveErr == nil {
		t.Fatal("move succeeded on a read-only vault")
	}
	if got := exitCode(moveErr); got != ExitVaultUnreach {
		t.Fatalf("exit code = %d, want %d (%v)", got, ExitVaultUnreach, moveErr)
	}
	if mover.JournalExists(vault, "claude-code", "data") {
		t.Fatal("a journal was created on a read-only vault")
	}
	after := snapshotTree(t, vault)
	if len(before) != len(after) {
		t.Fatalf("vault changed during a refused move: before=%v after=%v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("vault changed during a refused move: %q -> %q", before[i], after[i])
		}
	}
}
