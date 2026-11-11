// Package manifest records every successful move so status, restore and
// rollback always know the truth.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/midas-cli/midas/internal/version"
)

// SchemaVersion is the manifest format version.
const SchemaVersion = 1

// RestorePoint records a best-effort OS snapshot attempt.
type RestorePoint struct {
	Attempted bool   `json:"attempted"`
	Created   bool   `json:"created"`
	Reason    string `json:"reason,omitempty"`
	Name      string `json:"name,omitempty"`
	Sequence  int    `json:"sequence,omitempty"`
}

// BackupInfo links a manifest entry to its backup set.
type BackupInfo struct {
	ID           string       `json:"id"`
	Mode         string       `json:"mode"`
	State        string       `json:"state"`
	Path         string       `json:"path"`
	RestorePoint RestorePoint `json:"restore_point"`
}

// Entry is one moved target.
type Entry struct {
	App          string     `json:"app"`
	Target       string     `json:"target"`
	OriginalPath string     `json:"original_path"`
	VaultPath    string     `json:"vault_path"`
	LinkType     string     `json:"link_type"`
	MovedAt      time.Time  `json:"moved_at"`
	Bytes        uint64     `json:"bytes"`
	Files        int        `json:"files"`
	Verified     string     `json:"verified"`
	Backup       BackupInfo `json:"backup"`
}

// Manifest is the root document.
type Manifest struct {
	Schema       int     `json:"schema"`
	MidasVersion string  `json:"midas_version"`
	Entries      []Entry `json:"entries"`
}

// Empty returns a manifest with no entries.
func Empty() *Manifest {
	return &Manifest{Schema: SchemaVersion, MidasVersion: version.Version, Entries: []Entry{}}
}

// Path returns the manifest location inside a vault.
func Path(vault string) string {
	return filepath.Join(vault, ".midas", "manifest.json")
}

// Load reads the manifest; a missing file yields an empty manifest.
func Load(vault string) (*Manifest, error) {
	data, err := os.ReadFile(Path(vault))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty(), nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest is corrupt (%s): %w; run `midas doctor`", Path(vault), err)
	}
	if m.Schema == 0 {
		m.Schema = SchemaVersion
	}
	if m.Entries == nil {
		m.Entries = []Entry{}
	}
	return &m, nil
}

// Save writes the manifest atomically and keeps the previous file as .bak.
func (m *Manifest) Save(vault string) error {
	p := Path(vault)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	m.Schema = SchemaVersion
	if m.MidasVersion == "" {
		m.MidasVersion = version.Version
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(p), ".manifest-*")
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
	_ = os.Chmod(tmpName, 0o600)

	if _, err := os.Stat(p); err == nil {
		_ = os.Remove(p + ".bak")
		if err := os.Rename(p, p+".bak"); err != nil {
			return fmt.Errorf("preserve previous manifest: %w", err)
		}
	}
	if err := os.Rename(tmpName, p); err != nil {
		return err
	}
	return nil
}

// Find returns the entry for an app/target pair.
func (m *Manifest) Find(app, target string) (int, *Entry) {
	for i := range m.Entries {
		if m.Entries[i].App == app && m.Entries[i].Target == target {
			return i, &m.Entries[i]
		}
	}
	return -1, nil
}

// Upsert inserts or replaces an entry by app/target.
func (m *Manifest) Upsert(e Entry) {
	if i, _ := m.Find(e.App, e.Target); i >= 0 {
		m.Entries[i] = e
		return
	}
	m.Entries = append(m.Entries, e)
}

// Remove deletes an app/target entry.
func (m *Manifest) Remove(app, target string) {
	out := m.Entries[:0]
	for _, e := range m.Entries {
		if e.App == app && e.Target == target {
			continue
		}
		out = append(out, e)
	}
	m.Entries = out
}

// ForApp returns every entry belonging to an app.
func (m *Manifest) ForApp(app string) []Entry {
	var out []Entry
	for _, e := range m.Entries {
		if e.App == app {
			out = append(out, e)
		}
	}
	return out
}

// FindByBackupID returns the entry referencing a backup set.
func (m *Manifest) FindByBackupID(id string) (int, *Entry) {
	for i := range m.Entries {
		if m.Entries[i].Backup.ID == id {
			return i, &m.Entries[i]
		}
	}
	return -1, nil
}
