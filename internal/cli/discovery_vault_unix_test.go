//go:build unix

package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/mevarx/Midas/internal/config"
	"github.com/mevarx/Midas/internal/ui"
)

// TestListFailsClosedWhenRelativeVaultCannotResolve reproduces a configured
// relative vault whose absolute resolution fails because the working
// directory was deleted. Discovery must not mask that error: a configured
// vault always goes through full resolution and fails closed.
//
// This failure mode exists only on Unix: Windows' GetCurrentDirectory
// cannot fail for a live process, so the fixture is Unix-tagged. The
// cross-platform discovery contract is covered by list_test.go.
func TestListFailsClosedWhenRelativeVaultCannotResolve(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Remove(dir); err != nil {
		t.Fatalf("remove working directory: %v", err)
	}

	var stdout, stderr bytes.Buffer
	ctx := &Ctx{
		G:   Global{Vault: "relative-vault", JSON: true},
		Cfg: &config.Config{},
		UI:  &ui.UI{Out: &stdout, Err: &stderr, JSON: true},
	}
	if err := ctx.cmdList([]string{"--json"}); err == nil {
		t.Fatal("list entered discovery despite a configured vault that cannot resolve; want a hard error")
	}
}
