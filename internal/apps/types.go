package apps

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/midas-cli/midas/internal/platform"
)

// Definition is one app's declarative description. It is untrusted input:
// only data is honoured, never commands.
type Definition struct {
	ID          string            `toml:"id"`
	Name        string            `toml:"name"`
	Homepage    string            `toml:"homepage"`
	Description string            `toml:"description"`
	Process     ProcessRule       `toml:"process"`
	Launch      LaunchRule        `toml:"launch"`
	Targets     []Target          `toml:"target"`
	Env         map[string]string `toml:"env"`
	Backup      *BackupRule       `toml:"backup"`
	Notes       Notes             `toml:"notes"`

	// Source is the file this definition came from (empty for embedded).
	Source string `toml:"-"`
}

// ProcessRule describes how to tell whether the app is running.
type ProcessRule struct {
	Windows         []string `toml:"windows"`
	Linux           []string `toml:"linux"`
	Darwin          []string `toml:"darwin"`
	CmdlineContains []string `toml:"cmdline_contains"`
}

// LaunchRule is the command users normally type for this app.
type LaunchRule struct {
	Command string `toml:"command"`
}

// Target is one folder to relocate.
type Target struct {
	Name    string `toml:"name"`
	Windows string `toml:"windows"`
	Linux   string `toml:"linux"`
	Darwin  string `toml:"darwin"`
	Kind    string `toml:"kind"`
}

// BackupRule selects the files copied in essential backup mode.
type BackupRule struct {
	Include   []string `toml:"include"`
	Exclude   []string `toml:"exclude"`
	MaxFileMB int64    `toml:"max_file_mb"`
}

// Notes carries human advice shown by `apps show`.
type Notes struct {
	Text string `toml:"text"`
}

// ProcessNames returns executable names to match on the current OS.
func (d *Definition) ProcessNames() []string {
	switch platform.OS() {
	case "windows":
		return d.Process.Windows
	case "darwin":
		return d.Process.Darwin
	default:
		return d.Process.Linux
	}
}

// PathForOSE returns the raw (unexpanded) path for the given OS, or "".
func (t Target) PathForOSE(goos string) string {
	switch goos {
	case "windows":
		return t.Windows
	case "darwin":
		return t.Darwin
	default:
		return t.Linux
	}
}

// Path returns the raw path for the current OS.
func (t Target) Path() string { return t.PathForOSE(platform.OS()) }

// TargetByName finds a target by its name.
func (d *Definition) TargetByName(name string) (Target, bool) {
	for _, t := range d.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// TargetNames lists target names in definition order.
func (d *Definition) TargetNames() []string {
	out := make([]string, len(d.Targets))
	for i, t := range d.Targets {
		out[i] = t.Name
	}
	return out
}

// EffectiveBackupRule returns the definition's backup rule or the safe
// fallback required by the PRD (all files up to 25 MB except regenerable
// folders) when no [backup] section exists.
func (d *Definition) EffectiveBackupRule() BackupRule {
	if d.Backup != nil && (len(d.Backup.Include) > 0 || len(d.Backup.Exclude) > 0 || d.Backup.MaxFileMB > 0) {
		rule := *d.Backup
		if rule.MaxFileMB <= 0 {
			rule.MaxFileMB = 25
		}
		return rule
	}
	return BackupRule{
		Include:   []string{"**"},
		Exclude:   []string{"cache/**", "tmp/**", "logs/**", "**/node_modules/**"},
		MaxFileMB: 25,
	}
}

// RenderEnv applies the definition's [env] templates.
func (d *Definition) RenderEnv(vaultApp string) map[string]string {
	out := make(map[string]string, len(d.Env))
	for k, v := range d.Env {
		v = strings.ReplaceAll(v, "{{vault_app}}", vaultApp)
		v = strings.ReplaceAll(v, "{{tmp}}", filepath.Join(vaultApp, "tmp"))
		out[k] = v
	}
	return out
}

// Validate performs structural validation; path validation needs tokens and
// is done by ValidatePaths so callers can report all issues at once.
func (d *Definition) Validate() []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !validID(d.ID) {
		add("id %q is invalid: use lowercase letters, digits and hyphens only", d.ID)
	}
	if strings.TrimSpace(d.Name) == "" {
		add("name is required")
	}
	if len(d.Targets) == 0 {
		add("at least one [[target]] is required")
	}
	seen := map[string]bool{}
	for i, t := range d.Targets {
		if strings.TrimSpace(t.Name) == "" {
			add("target #%d has no name", i+1)
		} else if !validTargetName(t.Name) {
			add("target %q has an unsafe path component", t.Name)
		} else if seen[t.Name] {
			add("duplicate target name %q", t.Name)
		}
		seen[t.Name] = true
		if t.Kind != "dir" {
			add("target %q: kind must be \"dir\" (files are not supported)", t.Name)
		}
		if t.Windows == "" && t.Linux == "" && t.Darwin == "" {
			add("target %q: needs at least one of windows/linux/darwin", t.Name)
		}
	}
	if d.Process.CmdlineContains == nil {
		d.Process.CmdlineContains = []string{}
	}
	return errs
}

func validTargetName(name string) bool {
	if name == "." || name == ".." || name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	// No leading/trailing hyphen.
	return id[0] != '-' && id[len(id)-1] != '-'
}
