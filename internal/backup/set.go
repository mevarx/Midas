// Package backup implements the pre-move safety net: verified backup sets,
// their state machine, and the one protected deletion function.
package backup

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
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
	OperationID  string                `json:"operation_id,omitempty"`
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
	if s.OperationID == "" {
		id, err := newOperationID()
		if err != nil {
			return err
		}
		s.OperationID = id
	}
	if err := s.Validate(); err != nil {
		return err
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
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("backup set path %q is not a real directory", dir)
	}
	path := filepath.Join(dir, "BACKUP.json")
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("backup metadata %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Set
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("corrupt BACKUP.json in %s: %w", dir, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("corrupt BACKUP.json in %s: unexpected trailing value", dir)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("corrupt BACKUP.json in %s: %w", dir, err)
	}
	s.Dir = dir
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid BACKUP.json in %s: %w", dir, err)
	}
	return &s, nil
}

// Validate checks metadata before paths from BACKUP.json are joined to disk.
func (s *Set) Validate() error {
	if s.Schema != SchemaVersion || !validMetadataComponent(s.ID) || !validMetadataComponent(s.App) || !validMetadataComponent(s.Target) {
		return errors.New("invalid backup schema or identity")
	}
	if s.Dir != "" && filepath.Base(filepath.Clean(s.Dir)) != s.ID {
		return errors.New("backup ID does not match its directory")
	}
	if s.Dir != "" && filepath.Base(filepath.Dir(filepath.Clean(s.Dir))) != s.App {
		return errors.New("backup app does not match its directory")
	}
	if s.OperationID != "" && !validOperationID(s.OperationID) {
		return errors.New("invalid backup operation ID")
	}
	seen := make(map[string]bool, len(s.Files))
	seenFolded := make(map[string]bool, len(s.Files))
	for _, f := range s.Files {
		folded := strings.ToLower(f.Path)
		if !validBackupRelativePath(f.Path) || f.Size < 0 || seen[f.Path] || seenFolded[folded] {
			return fmt.Errorf("invalid or duplicate backup file path %q", f.Path)
		}
		seen[f.Path] = true
		seenFolded[folded] = true
	}
	for _, f := range s.Skipped {
		if !validBackupRelativePath(f.Path) || f.Size < 0 {
			return fmt.Errorf("invalid skipped-file path %q", f.Path)
		}
	}
	return nil
}

func validBackupRelativePath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\:\x00") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validMetadataComponent(value string) bool {
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

func validOperationID(value string) bool {
	if len(value) != 35 || !strings.HasPrefix(value, "op-") {
		return false
	}
	_, err := hex.DecodeString(value[3:])
	return err == nil
}

func newOperationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "op-" + hex.EncodeToString(b), nil
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
	path := s.LockPath()
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("backup lock %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
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
