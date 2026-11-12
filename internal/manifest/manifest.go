// Package manifest records every successful move so status, restore and
// rollback always know the truth.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mevarx/Midas/internal/platform"
	"github.com/mevarx/Midas/internal/version"
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
	OperationID  string     `json:"operation_id,omitempty"`
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
	meta := filepath.Join(vault, ".midas")
	if fi, err := os.Lstat(meta); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(meta) {
			return nil, fmt.Errorf("manifest directory %q is not a real directory", meta)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	path := Path(vault)
	if fi, err := os.Lstat(path); err == nil {
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("manifest %q is not a regular file", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty(), nil
		}
		return nil, err
	}
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is corrupt (%s): %w; run `midas doctor`", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("manifest has trailing data (%s): %w", path, err)
	}
	if m.Schema == 0 {
		m.Schema = SchemaVersion
	}
	if m.Entries == nil {
		m.Entries = []Entry{}
	}
	if err := m.validate(vault); err != nil {
		return nil, fmt.Errorf("manifest contains unsafe metadata: %w; preserve it and run `midas doctor`", err)
	}
	return &m, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("unexpected extra JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Save writes the manifest atomically and keeps the previous file as .bak.
func (m *Manifest) Save(vault string) error {
	p := Path(vault)
	meta := filepath.Dir(p)
	if fi, err := os.Lstat(meta); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(meta) {
			return fmt.Errorf("manifest directory %q is not a real directory", meta)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(meta, 0o700); err != nil {
			return err
		}
	} else {
		return err
	}
	metaInfo, err := os.Lstat(meta)
	if err != nil || !metaInfo.IsDir() || metaInfo.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(meta) {
		return fmt.Errorf("manifest directory %q is not a real directory", meta)
	}
	m.Schema = SchemaVersion
	if m.MidasVersion == "" {
		m.MidasVersion = version.Version
	}
	if err := m.validate(vault); err != nil {
		return fmt.Errorf("refusing to save unsafe manifest: %w", err)
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

	if fi, err := os.Lstat(p); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("manifest %q is not a regular file", p)
		}
		_ = os.Remove(p + ".bak")
		if err := os.Rename(p, p+".bak"); err != nil {
			return fmt.Errorf("preserve previous manifest: %w", err)
		}
	}
	if err := os.Rename(tmpName, p); err != nil {
		return err
	}
	// Both renames above (old manifest to .bak, temp file to the manifest
	// path) live in the same directory, so one flush covers them.
	if err := platform.SyncDir(meta); err != nil {
		return fmt.Errorf("make manifest %q durable: %w", p, err)
	}
	return nil
}

func (m *Manifest) validate(vault string) error {
	if m.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d", m.Schema)
	}
	seen := make(map[string]bool, len(m.Entries))
	root, err := filepath.Abs(filepath.Clean(vault))
	if err != nil {
		return err
	}
	for _, e := range m.Entries {
		if !safeComponent(e.App) || !safeComponent(e.Target) {
			return fmt.Errorf("invalid app/target identity %q/%q", e.App, e.Target)
		}
		key := e.App + "/" + e.Target
		if seen[key] {
			return fmt.Errorf("duplicate entry %s", key)
		}
		seen[key] = true
		if e.OperationID != "" && !validOperationID(e.OperationID) {
			return fmt.Errorf("entry %s has an invalid operation ID", key)
		}
		if !filepath.IsAbs(e.OriginalPath) || filepath.Clean(e.OriginalPath) != e.OriginalPath || !filepath.IsAbs(e.VaultPath) {
			return fmt.Errorf("entry %s has a non-absolute or non-normalized path", key)
		}
		expected := filepath.Join(root, "apps", e.App, e.Target)
		actual, err := filepath.Abs(filepath.Clean(e.VaultPath))
		if err != nil || actual != expected {
			return fmt.Errorf("entry %s destination is outside its expected vault path", key)
		}
		for _, dir := range []string{filepath.Join(root, "apps"), filepath.Join(root, "apps", e.App)} {
			fi, err := os.Lstat(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(dir) {
				return fmt.Errorf("entry %s has a vault directory that is not a real directory", key)
			}
		}
		rel, err := filepath.Rel(root, e.OriginalPath)
		if err != nil || rel == "." || rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("entry %s original path overlaps the vault", key)
		}
	}
	return nil
}

func validOperationID(value string) bool {
	if len(value) != 35 || !strings.HasPrefix(value, "op-") {
		return false
	}
	for _, r := range value[3:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func safeComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
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
