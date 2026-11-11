// Package backup implements the pre-move safety net: verified backup sets,
// their state machine, and the one protected deletion function.
package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/midas-cli/midas/internal/manifest"
)

// SchemaVersion is the BACKUP.json format version.
const SchemaVersion = 1

// Backup set states (Section 9.2.3).
const (
	StateCreating   = "creating"
	StateVerified   = "verified"
	StateInUse      = "in-use"
	StateComplete   = "complete"
	StateRolledBack = "rolled-back"
	StateAbandoned  = "abandoned"
)

// Modes.
const (
	ModeFull      = "full"
	ModeEssential = "essential"
	ModeNone      = "none"
)

// File is one backed-up file.
type File struct {
	Path   string    `json:"path"`
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime"`
	Mode   uint32    `json:"mode"`
	SHA256 string    `json:"sha256,omitempty"`
	Link   string    `json:"link,omitempty"` // symlink target; hash is empty
}

// Skipped is one file intentionally not backed up.
type Skipped struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"`
}

// Set describes one backup set.
type Set struct {
	Schema       int                   `json:"schema"`
	ID           string                `json:"id"`
	App          string                `json:"app"`
	Target       string                `json:"target"`
	OriginalPath string                `json:"original_path"`
	Mode         string                `json:"mode"`
	State        string                `json:"state"`
	CreatedAt    time.Time             `json:"created_at"`
	Files        []File                `json:"files"`
	Skipped      []Skipped             `json:"skipped"`
	RestorePoint manifest.RestorePoint `json:"restore_point"`

	// Dir is the set's location on disk (not serialized).
	Dir string `json:"-"`
}

// TotalBytes sums the backed-up file sizes.
func (s *Set) TotalBytes() int64 {
	var n int64
	for _, f := range s.Files {
		n += f.Size
	}
	return n
}

// StateLabel is a human-readable state.
func StateLabel(state string) string {
	switch state {
	case StateCreating:
		return "creating"
	case StateVerified:
		return "verified (move not finished)"
	case StateInUse:
		return "in use (move in progress)"
	case StateComplete:
		return "complete"
	case StateRolledBack:
		return "rolled back"
	case StateAbandoned:
		return "abandoned"
	default:
		return state
	}
}

// Manager owns the backup root and set operations.
type Manager struct {
	Dir string
}

// NewManager returns a manager rooted at dir.
func NewManager(dir string) *Manager { return &Manager{Dir: dir} }

// SetDir returns where a set lives.
func (m *Manager) SetDir(app, id string) string {
	return filepath.Join(m.Dir, app, id)
}

// LockPath returns the LOCK file path of a set.
func (s *Set) LockPath() string { return filepath.Join(s.Dir, "LOCK") }

// FilesDir returns the mirrored files directory.
func (s *Set) FilesDir() string { return filepath.Join(s.Dir, "files") }

// JSONPath returns BACKUP.json's location.
func (s *Set) JSONPath() string { return filepath.Join(s.Dir, "BACKUP.json") }

// Save writes BACKUP.json atomically.
func (s *Set) Save() error {
	if s.Dir == "" {
		return errors.New("backup set has no directory")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(s.JSONPath(), data, 0o600)
}

// LoadSet reads a set from its directory.
func LoadSet(dir string) (*Set, error) {
	data, err := os.ReadFile(filepath.Join(dir, "BACKUP.json"))
	if err != nil {
		return nil, err
	}
	var s Set
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupt BACKUP.json in %s: %w", dir, err)
	}
	s.Dir = dir
	return &s, nil
}

// List returns sets under the manager root, newest first.
func (m *Manager) List(appFilter string) ([]*Set, error) {
	var out []*Set
	apps, err := os.ReadDir(m.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, a := range apps {
		if !a.IsDir() {
			continue
		}
		if appFilter != "" && a.Name() != appFilter {
			continue
		}
		ids, err := os.ReadDir(filepath.Join(m.Dir, a.Name()))
		if err != nil {
			continue
		}
		for _, id := range ids {
			if !id.IsDir() {
				continue
			}
			s, err := LoadSet(filepath.Join(m.Dir, a.Name(), id.Name()))
			if err != nil {
				// Keep an unreadable set visible rather than hiding it.
				out = append(out, &Set{
					ID:    id.Name(),
					App:   a.Name(),
					State: "unreadable",
					Dir:   filepath.Join(m.Dir, a.Name(), id.Name()),
				})
				continue
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Find locates a set by id, optionally restricted to an app.
func (m *Manager) Find(id string) (*Set, error) {
	all, err := m.List("")
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("backup set %q not found", id)
}

// NewID returns a timestamp id, suffixed if a set with that id exists.
func (m *Manager) NewID(app string) string {
	base := time.Now().Format("20060102-150405")
	id := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(m.Dir, app, id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// Close loads the LOCK, if present.
func (s *Set) Lock() (*LockFile, error) {
	data, err := os.ReadFile(s.LockPath())
	if err != nil {
		return nil, err
	}
	var l LockFile
	if err := json.Unmarshal(data, &l); err != nil {
		return &LockFile{}, nil // unreadable lock still counts as held
	}
	return &l, nil
}

// LockFile identifies the process that holds a set.
type LockFile struct {
	PID       int32     `json:"pid"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
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
	_ = os.Chmod(name, mode)
	return os.Rename(name, path)
}
