package mover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/platform"
)

// MoveOptions describes one target move. The caller has already run
// pre-flight, created the verified backup set and confirmed with the user.
type MoveOptions struct {
	App               string
	Target            apps.Target
	Source            string
	Dest              string
	Vault             string
	Verify            string // "size" or "hash"
	Force             bool
	BackupSet         *backup.Set
	NoBackupConfirmed bool // set only when the user explicitly typed the confirmation
	SourceBytes       uint64
	SourceFiles       int
	Manifest          *manifest.Manifest
	Progress          Reporter
	Stage             func(stage, detail string)
	Warn              func(format string, a ...any)
}

// MoveTarget runs the Section 9.3 state machine for one target. On any
// failure after the safety net it performs an automatic rollback before
// returning.
func MoveTarget(o MoveOptions) (entry manifest.Entry, err error) {
	if o.BackupSet == nil && !o.NoBackupConfirmed {
		return entry, errors.New("refusing to move without a verified backup set")
	}
	if o.BackupSet != nil && o.BackupSet.State != backup.StateVerified && o.BackupSet.State != backup.StateInUse {
		return entry, fmt.Errorf("refusing to move: backup set is %s, not verified", o.BackupSet.State)
	}
	if o.Manifest == nil {
		return entry, errors.New("move requires a manifest")
	}
	if o.Stage == nil {
		o.Stage = func(string, string) {}
	}
	if o.Warn == nil {
		o.Warn = func(string, ...any) {}
	}
	parent := filepath.Dir(o.Dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return entry, fmt.Errorf("create vault app directory: %w", err)
	}
	if PathExists(o.Dest) {
		return entry, fmt.Errorf("destination %q already exists; refusing to overwrite", o.Dest)
	}
	bak := o.Source + ".midas-bak"
	partial := o.Dest + ".midas-partial"
	if PathExists(bak) {
		return entry, fmt.Errorf("a previous backup exists at %q; run `midas doctor` first", bak)
	}
	if err := os.RemoveAll(partial); err != nil {
		return entry, err
	}

	j := &Journal{
		App: o.App, Target: o.Target.Name, State: StateCopying,
		Source: o.Source, Dest: o.Dest, Partial: partial, Bak: bak,
	}
	if o.BackupSet != nil {
		j.BackupID = o.BackupSet.ID
	}
	if err := SaveJournal(o.Vault, j); err != nil {
		return entry, fmt.Errorf("write journal: %w", err)
	}
	success := false
	defer func() {
		if err != nil && !success {
			rb := RollbackOptions{
				App: o.App, Target: o.Target.Name, Vault: o.Vault,
				Source: o.Source, Dest: o.Dest, BackupSet: o.BackupSet,
				Manifest: o.Manifest, Force: true, Automatic: true,
				Warn: o.Warn,
			}
			if _, rbErr := Rollback(rb); rbErr != nil {
				backupDir := "(none: --backup=none)"
				if o.BackupSet != nil {
					backupDir = o.BackupSet.Dir
				}
				err = fmt.Errorf("%w\n\nautomatic rollback also failed: %v\n"+
					"your data is still at: original=%s, vault copy=%s, backup=%s\n"+
					"see docs/SAFETY.md for manual recovery", err, rbErr, o.Source, o.Dest, backupDir)
			}
		}
	}()

	if o.BackupSet != nil {
		if err := o.BackupSet.MarkInUse(); err != nil {
			return entry, fmt.Errorf("mark backup in use: %w", err)
		}
	}

	sameVolume, err := SameVolume(o.Source, parent)
	if err != nil {
		o.Warn("could not determine whether the move is on one volume (%v); using the copy path", err)
		sameVolume = false
	}

	if sameVolume {
		// Step 5 shortcut: a plain rename is atomic and preserves everything.
		o.Stage(StateRenaming, "renaming into the vault (same volume)")
		j.State = StateRenaming
		if err := SaveJournal(o.Vault, j); err != nil {
			return entry, err
		}
		if err := os.Rename(o.Source, o.Dest); err != nil {
			return entry, fmt.Errorf("rename %q to %q: %w", o.Source, o.Dest, err)
		}
	} else {
		o.Stage(StateCopying, "copying")
		if _, cerr := CopyTree(o.Source, partial, CopyOptions{Progress: o.Progress}); cerr != nil {
			return entry, fmt.Errorf("copy failed, original untouched: %w", cerr)
		}
		j.State = StateVerifying
		o.Stage(StateVerifying, "verifying copy")
		if err := SaveJournal(o.Vault, j); err != nil {
			return entry, err
		}
		if _, verr := VerifyTree(o.Source, partial, o.Verify == "hash"); verr != nil {
			return entry, fmt.Errorf("%w: %v", ErrVerifyFailed, verr)
		}
		j.State = StateRenaming
		o.Stage(StateRenaming, "moving verified copy into place")
		if err := SaveJournal(o.Vault, j); err != nil {
			return entry, err
		}
		if err := os.Rename(partial, o.Dest); err != nil {
			return entry, fmt.Errorf("rename partial copy into place: %w", err)
		}
	}

	// Steps 6-7: swap the original aside and link it. On the same volume the
	// verified rename above already moved the original, so there is nothing
	// to swap aside.
	j.State = StateSwapping
	o.Stage(StateSwapping, "swapping original aside")
	if err := SaveJournal(o.Vault, j); err != nil {
		return entry, err
	}
	if !sameVolume {
		if err := os.Rename(o.Source, bak); err != nil {
			return entry, fmt.Errorf("rename original to %q (files in use?): %w", bak, err)
		}
	}
	if err := platform.Current().CreateLink(o.Source, o.Dest); err != nil {
		return entry, fmt.Errorf("create link at %q: %w", o.Source, err)
	}

	j.State = StateLinking
	o.Stage(StateLinking, "health-checking link")
	if err := SaveJournal(o.Vault, j); err != nil {
		return entry, err
	}
	if ok, detail := HealthCheck(o.Source, o.Dest); !ok {
		return entry, fmt.Errorf("link health check failed: %s", detail)
	}

	// Step 10: record the move.
	entry = manifest.Entry{
		App:          o.App,
		Target:       o.Target.Name,
		OriginalPath: o.Source,
		VaultPath:    o.Dest,
		LinkType:     platform.LinkType(),
		MovedAt:      time.Now().UTC(),
		Bytes:        o.SourceBytes,
		Files:        o.SourceFiles,
		Verified:     o.Verify,
		Backup: manifest.BackupInfo{
			Mode:  backup.ModeNone,
			State: backup.ModeNone,
		},
	}
	if o.BackupSet != nil {
		if entry.Bytes == 0 {
			entry.Bytes = uint64(o.BackupSet.TotalBytes())
		}
		if entry.Files == 0 {
			entry.Files = len(o.BackupSet.Files)
		}
		entry.Backup = manifest.BackupInfo{
			ID:           o.BackupSet.ID,
			Mode:         o.BackupSet.Mode,
			State:        backup.StateInUse,
			Path:         o.BackupSet.Dir,
			RestorePoint: o.BackupSet.RestorePoint,
		}
	}
	o.Manifest.Upsert(entry)
	if err := o.Manifest.Save(o.Vault); err != nil {
		return entry, fmt.Errorf("write manifest: %w", err)
	}

	// Step 11: drop the untouched original.
	j.State = StateCleanup
	_ = SaveJournal(o.Vault, j)
	if err := os.RemoveAll(bak); err != nil {
		o.Warn("could not remove %s (%v); it is safe to delete by hand or with `midas doctor`", bak, err)
	}

	// Step 12: finish.
	if o.BackupSet != nil {
		if err := o.BackupSet.MarkComplete(); err != nil {
			o.Warn("could not update backup state to complete: %v", err)
		}
		entry.Backup.State = backup.StateComplete
		o.Manifest.Upsert(entry)
		if err := o.Manifest.Save(o.Vault); err != nil {
			o.Warn("could not persist final manifest state: %v", err)
		}
	}
	if err := RemoveJournal(o.Vault, o.App, o.Target.Name); err != nil {
		o.Warn("could not remove journal: %v", err)
	}
	success = true
	return entry, nil
}
