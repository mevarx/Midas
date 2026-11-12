package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mevarx/Midas/internal/platform"
)

const SchemaVersion = 1

// Config is the user configuration stored in config.toml.
type Config struct {
	SchemaVersion   int    `toml:"schema_version"`
	Vault           string `toml:"vault"`
	Verify          string `toml:"verify"`           // size | hash
	Confirm         bool   `toml:"confirm"`          // ask before destructive work
	ShimDir         string `toml:"shim_dir"`         // where shims are written
	AllowFallback   bool   `toml:"allow_fallback"`   // allow temp fallback in run
	Backup          string `toml:"backup"`           // full | essential | none
	BackupDir       string `toml:"backup_dir"`       // "" => <vault>/backup
	RestorePoint    string `toml:"restore_point"`    // auto | require | skip
	BackupRetention string `toml:"backup_retention"` // keep | delete-on-success | 30d

	// Path is where this config was loaded from (not serialized).
	Path string `toml:"-"`
}

// Defaults returns the built-in configuration.
func Defaults() *Config {
	return &Config{
		SchemaVersion:   SchemaVersion,
		Verify:          "size",
		Confirm:         true,
		Backup:          "full",
		RestorePoint:    "auto",
		BackupRetention: "keep",
	}
}

// DefaultPath returns the OS-appropriate config file location.
func DefaultPath() string {
	if p := os.Getenv("MIDAS_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(platform.Current().ConfigDir(), "config.toml")
}

// Load reads config from path (empty = default). A missing file is not an
// error; defaults are returned with the effective path set.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	c := Defaults()
	c.Path = path
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			applyEnv(c)
			return c, nil
		}
		return nil, err
	}
	md, err := toml.Decode(string(data), c)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	c.Path = path
	applyEnv(c)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("MIDAS_VAULT"); v != "" {
		c.Vault = v
	}
}

// Validate checks enumerated values.
func (c *Config) Validate() error {
	var errs []error
	if c.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Errorf("unsupported config schema_version %d (supported: %d)", c.SchemaVersion, SchemaVersion))
	}
	switch c.Verify {
	case "size", "hash":
	default:
		errs = append(errs, fmt.Errorf("verify must be \"size\" or \"hash\", got %q", c.Verify))
	}
	switch c.Backup {
	case "full", "essential", "none":
	default:
		errs = append(errs, fmt.Errorf("backup must be full, essential or none, got %q", c.Backup))
	}
	switch c.RestorePoint {
	case "auto", "require", "skip":
	default:
		errs = append(errs, fmt.Errorf("restore_point must be auto, require or skip, got %q", c.RestorePoint))
	}
	if !validRetention(c.BackupRetention) {
		errs = append(errs, fmt.Errorf("backup_retention must be keep, delete-on-success or <N>d, got %q", c.BackupRetention))
	}
	return errors.Join(errs...)
}

func validRetention(s string) bool {
	if s == "keep" || s == "delete-on-success" {
		return true
	}
	if len(s) < 2 || !strings.HasSuffix(s, "d") {
		return false
	}
	for _, r := range s[:len(s)-1] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Save writes the config atomically, creating its directory.
func (c *Config) Save() error {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	if err := c.Validate(); err != nil {
		return err
	}
	path := c.Path
	if path == "" {
		path = DefaultPath()
		c.Path = path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Midas configuration\n")
	fmt.Fprintf(&b, "schema_version = %d\n", c.SchemaVersion)
	fmt.Fprintf(&b, "vault = %s\n", tomlString(c.Vault))
	fmt.Fprintf(&b, "verify = %s\n", tomlString(c.Verify))
	fmt.Fprintf(&b, "confirm = %v\n", c.Confirm)
	fmt.Fprintf(&b, "shim_dir = %s\n", tomlString(c.ShimDir))
	fmt.Fprintf(&b, "allow_fallback = %v\n", c.AllowFallback)
	fmt.Fprintf(&b, "backup = %s\n", tomlString(c.Backup))
	fmt.Fprintf(&b, "backup_dir = %s\n", tomlString(c.BackupDir))
	fmt.Fprintf(&b, "restore_point = %s\n", tomlString(c.RestorePoint))
	fmt.Fprintf(&b, "backup_retention = %s\n", tomlString(c.BackupRetention))
	return atomicWrite(path, []byte(b.String()), 0o600)
}

func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".midas-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// EffectiveBackupDir resolves backup_dir with the <vault>/backup fallback.
func (c *Config) EffectiveBackupDir(vault string) string {
	if c.BackupDir != "" {
		return c.BackupDir
	}
	return filepath.Join(vault, "backup")
}
