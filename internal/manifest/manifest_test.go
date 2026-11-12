package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mevarx/Midas/internal/platform"
)

func TestLoadRejectsManifestDestinationTraversal(t *testing.T) {
	vault := t.TempDir()
	manifest := &Manifest{
		Schema:  SchemaVersion,
		Entries: []Entry{{App: "app", Target: "config", OriginalPath: filepath.Join(t.TempDir(), "original"), VaultPath: filepath.Join(vault, "outside", "target")}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(vault)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(vault), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(vault); err == nil {
		t.Fatal("manifest destination outside the app vault was accepted")
	}
}

func TestLoadRejectsMetadataDirectorySymlink(t *testing.T) {
	vault := t.TempDir()
	external := t.TempDir()
	if err := platform.Current().CreateLink(filepath.Join(vault, ".midas"), external); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	if _, err := Load(vault); err == nil {
		t.Fatal("manifest metadata directory symlink was followed")
	}
}

func TestLoadRejectsUnknownRecoveryMetadataFields(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(vault)), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":1,"midas_version":"test","entries":[],"future_recovery_field":true}`)
	if err := os.WriteFile(Path(vault), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(vault); err == nil {
		t.Fatal("unknown recovery metadata field was silently discarded")
	}
	got, err := os.ReadFile(Path(vault))
	if err != nil || string(got) != string(data) {
		t.Fatalf("unknown metadata was not preserved: %q, %v", got, err)
	}
}
