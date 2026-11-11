package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midas-cli/midas/internal/config"
	"github.com/midas-cli/midas/internal/ui"
)

func TestDoctorDefaultIsReadOnlyForInvalidManifest(t *testing.T) {
	vault := t.TempDir()
	meta := filepath.Join(vault, ".midas")
	if err := os.MkdirAll(meta, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(meta, "manifest.json")
	original := []byte("{invalid")
	if err := os.WriteFile(manifestPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	ctx := &Ctx{G: Global{Vault: vault, JSON: true}, Cfg: &config.Config{}, UI: &ui.UI{Out: &out, Err: &stderr}}
	if err := ctx.cmdDoctor([]string{"--json"}); err == nil {
		t.Fatal("invalid manifest should be unhealthy")
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("doctor modified invalid manifest: %q, %v", got, err)
	}
	if !strings.Contains(out.String(), `"code": "manifest.invalid"`) {
		t.Fatalf("JSON report missing stable manifest finding: %s", out.String())
	}
}
