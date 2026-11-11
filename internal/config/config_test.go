package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLegacyConfigDefaultsSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("verify = \"hash\"\nbackup = \"full\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion || got.Verify != "hash" {
		t.Fatalf("legacy config load = %#v", got)
	}
}

func TestLoadRejectsUnknownConfigSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("schema_version = 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported config schema_version") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestSaveWritesVersionedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := Defaults()
	c.Path = path
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.SchemaVersion != SchemaVersion {
		t.Fatalf("saved config load = %#v, %v", got, err)
	}
}
