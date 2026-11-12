// Package mover implements the journaled move/restore state machine and the
// copy/verify primitives it is built from.
package mover

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mevarx/Midas/internal/platform"
)

// Journal states.
const (
	StateCopying       = "copying"
	StateVerifying     = "verifying"
	StateRenaming      = "renaming"
	StateSwapping      = "swapping"
	StateLinking       = "linking"
	StateCleanup       = "cleanup"
	StateDone          = "done"
	StateRestoring     = "restoring"
	StateRollingBack   = "rolling_back"
	StateRolledBack    = "rolled_back"
	StateNeedsRecovery = "needs_recovery"
)

// Journal records one target's in-progress move so it can be resumed or
// rolled back after a crash.
type Journal struct {
	Schema            int       `json:"schema"`
	OperationID       string    `json:"operation_id,omitempty"`
	ParentOperationID string    `json:"parent_operation_id,omitempty"`
	Operation         string    `json:"operation,omitempty"`
	App               string    `json:"app"`
	Target            string    `json:"target"`
	State             string    `json:"state"`
	Source            string    `json:"source"`
	Dest              string    `json:"dest"`
	Partial           string    `json:"partial"`
	Bak               string    `json:"bak"`
	BackupID          string    `json:"backup_id"`
	NoBackup          bool      `json:"no_backup_confirmed,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// JournalPath returns the journal file for an app/target inside a vault.
func JournalPath(vault, app, target string) string {
	if !validJournalComponent(app) || !validJournalComponent(target) {
		return ""
	}
	return filepath.Join(vault, ".midas", "journal", app+"-"+target+".json")
}

func validJournalComponent(value string) bool {
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

// JournalExists reports whether a journal is present.
func JournalExists(vault, app, target string) bool {
	_, err := os.Lstat(JournalPath(vault, app, target))
	return err == nil
}

// LoadJournal reads a journal. A missing file returns (nil, nil).
func LoadJournal(vault, app, target string) (*Journal, error) {
	path := JournalPath(vault, app, target)
	if path == "" {
		return nil, fmt.Errorf("invalid journal identity %q/%q", app, target)
	}
	if err := validateJournalDirs(vault, false); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("journal %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	j, err := decodeJournal(data)
	if err != nil {
		return nil, fmt.Errorf("journal for %s/%s is unreadable: %w", app, target, err)
	}
	if err := validateJournal(vault, j, app, target); err != nil {
		return nil, fmt.Errorf("journal for %s/%s is invalid: %w", app, target, err)
	}
	return j, nil
}

// SaveJournal writes the journal atomically, replacing any existing file.
func SaveJournal(vault string, j *Journal) error {
	return writeJournal(vault, j, false)
}

// CreateJournal writes a new journal and refuses to overwrite an existing
// one, so two operations for the same app/target cannot silently replace each
// other's state.
func CreateJournal(vault string, j *Journal) error {
	return writeJournal(vault, j, true)
}

// writeJournal is the single write path for both journal creation and
// updates. Exclusive creation uses O_EXCL; updates keep the atomic
// CreateTemp+Sync+Rename semantics.
func writeJournal(vault string, j *Journal, exclusive bool) error {
	data, path, err := prepareJournal(vault, j)
	if err != nil {
		return err
	}
	if err := validateJournalDirs(vault, true); err != nil {
		return err
	}
	if exclusive {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return fmt.Errorf("journal for %s/%s already exists at %s; another Midas operation may be running for this target: %w", j.App, j.Target, path, err)
			}
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
		return f.Close()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".journal-*")
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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if err := platform.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("make journal %q durable: %w", path, err)
	}
	return nil
}

// prepareJournal validates j, fills in generated metadata, and returns the
// encoded bytes and target path shared by both write paths.
func prepareJournal(vault string, j *Journal) ([]byte, string, error) {
	if !validJournalComponent(j.App) || !validJournalComponent(j.Target) {
		return nil, "", fmt.Errorf("invalid journal identity %q/%q", j.App, j.Target)
	}
	if j.OperationID == "" {
		id, err := NewOperationID()
		if err != nil {
			return nil, "", err
		}
		j.OperationID = id
	}
	if !validOperationID(j.OperationID) {
		return nil, "", errors.New("invalid journal operation ID")
	}
	j.Schema = 1
	j.UpdatedAt = time.Now().UTC()
	if err := validateJournal(vault, j, j.App, j.Target); err != nil {
		return nil, "", err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return nil, "", err
	}
	return data, JournalPath(vault, j.App, j.Target), nil
}

// RemoveJournal deletes a journal (only after the move reached a safe state).
func RemoveJournal(vault, app, target string) error {
	err := os.Remove(JournalPath(vault, app, target))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ListJournals returns every journal in the vault.
func ListJournals(vault string) ([]*Journal, error) {
	journals, issues, err := ScanJournals(vault)
	if err != nil {
		return journals, err
	}
	if len(issues) > 0 {
		return journals, fmt.Errorf("%d invalid journal file(s): %s", len(issues), strings.Join(issues, ", "))
	}
	return journals, nil
}

// ScanJournals returns valid journals and paths that must be preserved for
// diagnosis instead of silently skipping malformed metadata.
func ScanJournals(vault string) ([]*Journal, []string, error) {
	dir := filepath.Join(vault, ".midas", "journal")
	if err := validateJournalDirs(vault, false); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var out []*Journal
	var issues []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, path)
			continue
		}
		j, decodeErr := decodeJournal(data)
		if decodeErr != nil || validateJournal(vault, j, j.App, j.Target) != nil || JournalPath(vault, j.App, j.Target) != path {
			issues = append(issues, path)
			continue
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	sort.Strings(issues)
	return out, issues, nil
}

func decodeJournal(data []byte) (*Journal, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal Journal
	if err := decoder.Decode(&journal); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, errors.New("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return &journal, nil
}

func validateJournalDirs(vault string, create bool) error {
	meta := filepath.Join(vault, ".midas")
	journal := filepath.Join(meta, "journal")
	for _, dir := range []string{meta, journal} {
		fi, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			fi, err = os.Lstat(dir)
		}
		if errors.Is(err, os.ErrNotExist) && !create {
			return nil
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(dir) {
			return fmt.Errorf("journal directory %q is not a real directory", dir)
		}
	}
	return nil
}

func validateJournal(vault string, j *Journal, app, target string) error {
	if j == nil || !validJournalComponent(app) || !validJournalComponent(target) || j.App != app || j.Target != target {
		return errors.New("invalid app or target identity")
	}
	if j.Schema != 1 || !validJournalState(j.State) {
		return errors.New("unsupported journal schema or state")
	}
	if j.OperationID != "" && !validOperationID(j.OperationID) || j.ParentOperationID != "" && !validOperationID(j.ParentOperationID) {
		return errors.New("invalid operation identity")
	}
	if j.Operation != "" && j.Operation != "move" && j.Operation != "restore" && j.Operation != "rollback" {
		return errors.New("unknown operation type")
	}
	if j.Operation != "" && (j.Source == "" || j.Dest == "" || j.Partial == "") {
		return errors.New("operation journal is missing required paths")
	}
	if j.BackupID != "" && !validJournalComponent(j.BackupID) || j.NoBackup && j.BackupID != "" {
		return errors.New("invalid backup reference")
	}
	vault, err := filepath.Abs(filepath.Clean(vault))
	if err != nil {
		return err
	}
	expectedDest := filepath.Join(vault, "apps", app, target)
	if j.Dest != "" {
		dest, err := filepath.Abs(filepath.Clean(j.Dest))
		if err != nil || dest != expectedDest {
			return errors.New("destination is outside its expected vault path")
		}
	}
	if j.Source != "" {
		if !filepath.IsAbs(j.Source) || filepath.Clean(j.Source) != j.Source {
			return errors.New("source path is not absolute and normalized")
		}
		rel, err := filepath.Rel(vault, j.Source)
		if err != nil || rel == "." || rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("source path overlaps the vault")
		}
	}
	if j.Partial != "" {
		partial, err := filepath.Abs(filepath.Clean(j.Partial))
		if err != nil || partial != j.Partial || j.Source != "" && partial != j.Source+".midas-partial" && (j.Dest == "" || partial != j.Dest+".midas-partial") {
			return errors.New("partial path does not match the recorded source or destination")
		}
	}
	if j.Bak != "" && (j.Source == "" || j.Bak != j.Source+".midas-bak") {
		return errors.New("original backup path does not match the recorded source")
	}
	if j.Operation == "move" && j.Bak != j.Source+".midas-bak" || j.Operation == "restore" && j.Bak != "" || j.Operation == "rollback" && j.Bak != j.Source+".midas-bak" {
		return errors.New("journal backup path does not match the operation type")
	}
	return nil
}

func validJournalState(state string) bool {
	switch state {
	case StateCopying, StateVerifying, StateRenaming, StateSwapping, StateLinking, StateCleanup, StateDone, StateRestoring, StateRollingBack, StateRolledBack, StateNeedsRecovery:
		return true
	default:
		return false
	}
}
