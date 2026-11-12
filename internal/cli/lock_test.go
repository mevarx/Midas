package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/config"
	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/platform"
	"github.com/mevarx/Midas/internal/ui"
)

func TestCmdMoveRefusesHeldVaultLock(t *testing.T) {
	vault := t.TempDir()
	meta := filepath.Join(vault, ".midas")
	if err := os.MkdirAll(meta, 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := platform.Current().AcquireLock(filepath.Join(meta, "LOCK"))
	if err != nil {
		t.Fatalf("hold vault lock: %v", err)
	}
	t.Cleanup(func() { _ = handle.Release() })

	defs, err := apps.Builtins()
	if err != nil {
		t.Fatalf("builtin apps: %v", err)
	}
	var def *apps.Definition
	for _, d := range defs {
		if d.ID == "claude-code" {
			def = d
			break
		}
	}
	if def == nil {
		t.Fatal("builtin claude-code definition is missing")
	}
	var sources []string
	for _, target := range def.Targets {
		if target.Path() == "" {
			continue
		}
		src, err := apps.ExpandTarget(target)
		if err != nil {
			t.Fatalf("expand target %s: %v", target.Name, err)
		}
		sources = append(sources, src)
	}
	existed := make(map[string]bool, len(sources))
	for _, src := range sources {
		_, statErr := os.Lstat(src)
		existed[src] = statErr == nil
	}

	var out, stderr bytes.Buffer
	ctx := &Ctx{
		G:   Global{Vault: vault},
		Cfg: &config.Config{Vault: vault, Backup: "none", Verify: "size"},
		UI:  &ui.UI{Out: &out, Err: &stderr},
	}
	moveErr := ctx.cmdMove([]string{"claude-code"})
	if moveErr == nil {
		t.Fatal("move succeeded while the vault lock was held")
	}
	if got := exitCode(moveErr); got != ExitGeneral {
		t.Fatalf("exit code = %d, want %d (%v)", got, ExitGeneral, moveErr)
	}
	msg := strings.ToLower(moveErr.Error())
	if !strings.Contains(msg, "another midas operation") || !strings.Contains(msg, "using vault") {
		t.Fatalf("lock contention message is unclear: %v", moveErr)
	}
	if mover.JournalExists(vault, "claude-code", "data") {
		t.Fatal("a journal was created despite lock contention")
	}
	for _, src := range sources {
		if _, statErr := os.Lstat(src); (statErr == nil) != existed[src] {
			t.Fatalf("source %s changed during a refused move", src)
		}
	}
}
