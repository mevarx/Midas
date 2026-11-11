package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midas-cli/midas/internal/config"
	"github.com/midas-cli/midas/internal/ui"
)

func TestPlanIsReadOnlyAndVersionedJSON(t *testing.T) {
	vault := filepath.Join(t.TempDir(), "not-created")
	var stdout, stderr bytes.Buffer
	ctx := &Ctx{
		G:   Global{Vault: vault, JSON: true},
		Cfg: &config.Config{Backup: "none", Verify: "size"},
		UI:  &ui.UI{Out: &stdout, Err: &stderr, JSON: true},
	}
	if err := ctx.cmdPlan([]string{"--json", "claude-code"}); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["read_only"] != true || report["schema_version"] != float64(1) {
		t.Fatalf("unexpected plan report: %v", report)
	}
	if _, err := os.Lstat(vault); !os.IsNotExist(err) {
		t.Fatalf("plan created or modified vault %q: %v", vault, err)
	}
}

func TestCompletionScriptsAreStaticAndAvailable(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			var out, stderr bytes.Buffer
			ctx := &Ctx{UI: &ui.UI{Out: &out, Err: &stderr}}
			if err := ctx.cmdCompletion([]string{shell}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "midas") || !strings.Contains(out.String(), "plan") || strings.Contains(out.String(), "apps list") {
				t.Fatalf("completion is missing commands or appears to perform runtime scans: %s", out.String())
			}
		})
	}
}
