// Package mover implements the journaled move/restore state machine and the
// copy/verify primitives it is built from.
package mover

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Journal states (Section 9.3).
const (
	StateCopying   = "copying"
	StateVerifying = "verifying"
	StateRenaming  = "renaming"
	StateSwapping  = "swapping"
	StateLinking   = "linking"
	StateCleanup   = "cleanup"
	StateDone      = "done"
)

// Journal records one target's in-progress move so it can be resumed or
// rolled back after a crash.
type Journal struct {
	Schema    int       `json:"schema"`
	App       string    `json:"app"`
	Target    string    `json:"target"`
	State     string    `json:"state"`
	Source    string    `json:"source"`
	Dest      string    `json:"dest"`
	Partial   string    `json:"partial"`
	Bak       string    `json:"bak"`
	BackupID  string    `json:"backup_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// JournalPath returns the journal file for an app/target inside a vault.
func JournalPath(vault, app, target string) string {
	return filepath.Join(vault, ".midas", "journal", app+"-"+target+".json")
}

// JournalExists reports whether a journal is present.
func JournalExists(vault, app, target string) bool {
	_, err := os.Lstat(JournalPath(vault, app, target))
	return err == nil
}

// LoadJournal reads a journal. A missing file returns (nil, nil).
func LoadJournal(vault, app, target string) (*Journal, error) {
	data, err := os.ReadFile(JournalPath(vault, app, target))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("journal for %s/%s is unreadable: %w", app, target, err)
	}
	return &j, nil
}

// SaveJournal writes the journal atomically.
func SaveJournal(vault string, j *Journal) error {
	j.Schema = 1
	j.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	path := JournalPath(vault, j.App, j.Target)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
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
	return os.Rename(name, path)
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
	dir := filepath.Join(vault, ".midas", "journal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Journal
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var j Journal
		if err := json.Unmarshal(data, &j); err != nil {
			continue
		}
		out = append(out, &j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
