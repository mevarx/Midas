package mover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
)

// MoveOptions describes one target move. The caller has already run
// pre-flight, created the verified backup set and confirmed with the user.
type MoveOptions struct {
	App               string
	Target            apps.Target
	Resume            bool
	OperationID       string
	Context           context.Context
	Workers           int
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
	VerifyProgress    Reporter
	Stage             func(stage, detail string)
	Warn              func(format string, a ...any)
	// Recheck is a point-in-time app-running check re-run before destructive
	// transitions. It is called immediately before the original is swapped
	// aside and again immediately before the untouched original is removed.
	// A non-nil error aborts the move into the automatic rollback. A nil
	// Recheck means no check is performed.
	Recheck func() error
}

// sameVolumeCheck is the volume decision used by MoveTarget. It exists only
// so tests can exercise the cross-volume copy path on a single-volume runner;
// production never assigns it.
var sameVolumeCheck = SameVolume

// MoveTarget runs the journaled move state machine for one target. On any
// failure after the safety net it performs an automatic rollback before
// returning.
func MoveTarget(o MoveOptions) (entry manifest.Entry, err error) {
	if o.BackupSet == nil && !o.NoBackupConfirmed {
		return entry, errors.New("refusing to move without a verified backup set")
	}
	if o.BackupSet != nil && o.BackupSet.State != backup.StateVerified && o.BackupSet.State != backup.StateInUse {
		return entry, fmt.Errorf("refusing to move: backup set is %s, not verified", o.BackupSet.State)
	}
	if o.BackupSet != nil {
		if err := backup.Verify(o.BackupSet); err != nil {
			return entry, fmt.Errorf("refusing to move with an invalid safety backup: %w", err)
		}
	}
	if o.Manifest == nil {
		return entry, errors.New("move requires a manifest")
	}
	sourceSnapshot, err := TreeSnapshot(o.Source)
	if err != nil {
		return entry, fmt.Errorf("inspect source before moving: %w", err)
	}
	if o.Stage == nil {
		o.Stage = func(string, string) {}
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	if o.Warn == nil {
		o.Warn = func(string, ...any) {}
	}
	parent := filepath.Dir(o.Dest)
	if err := EnsureVaultAppDir(o.Vault, o.App); err != nil {
		return entry, fmt.Errorf("validate vault app directory: %w", err)
	}
	if PathExists(o.Dest) {
		return entry, fmt.Errorf("destination %q already exists; refusing to overwrite", o.Dest)
	}
	bak := o.Source + ".midas-bak"
	partial := o.Dest + ".midas-partial"
	if PathExists(bak) || PathExists(ownerPath(bak)) {
		return entry, fmt.Errorf("a previous backup exists at %q; run `midas doctor` first", bak)
	}
	journal, err := LoadJournal(o.Vault, o.App, o.Target.Name)
	if err != nil {
		return entry, fmt.Errorf("inspect existing journal: %w", err)
	}
	operationID := o.OperationID
	if journal != nil {
		if !o.Resume {
			return entry, fmt.Errorf("an operation journal already exists for %s/%s; run `midas doctor` or `midas resume %s`", o.App, o.Target.Name, o.App)
		}
		if journal.Operation != "move" || (journal.State != StateCopying && journal.State != StateVerifying) || !validOperationID(journal.OperationID) || !platform.SamePath(journal.Source, o.Source) || !platform.SamePath(journal.Dest, o.Dest) || journal.BackupID != backupID(o.BackupSet) || journal.NoBackup != o.NoBackupConfirmed {
			return entry, fmt.Errorf("journal state or paths are not eligible for safe resume; preserve all copies and use `midas rollback %s`", o.App)
		}
		if !moverRealDirectory(o.Source) || PathExists(o.Dest) {
			return entry, fmt.Errorf("source/destination state is not safe for resume; no paths were changed")
		}
		if PathExists(partial) || PathExists(ownerPath(partial)) {
			if err := RemoveOwnedPath(filepath.Dir(partial), partial, journal.OperationID, "move-partial"); err != nil {
				return entry, fmt.Errorf("cannot safely restart the partial copy: %w", err)
			}
		}
		operationID = journal.OperationID
	} else {
		if o.Resume {
			return entry, fmt.Errorf("no resumable move journal exists for %s/%s", o.App, o.Target.Name)
		}
		if PathExists(partial) || PathExists(ownerPath(partial)) {
			return entry, fmt.Errorf("partial path %q already exists; preserving it; run `midas doctor` to inspect recovery state", partial)
		}
		if operationID == "" {
			operationID, err = NewOperationID()
			if err != nil {
				return entry, err
			}
		}
	}

	j := &Journal{
		OperationID: operationID, Operation: "move", App: o.App, Target: o.Target.Name, State: StateCopying, NoBackup: o.NoBackupConfirmed,
		Source: o.Source, Dest: o.Dest, Partial: partial, Bak: bak,
	}
	if o.BackupSet != nil {
		j.BackupID = o.BackupSet.ID
	}
	if journal == nil {
		if err := CreateJournal(o.Vault, j); err != nil {
			return entry, fmt.Errorf("write journal: %w", err)
		}
	} else if err := SaveJournal(o.Vault, j); err != nil {
		return entry, fmt.Errorf("write journal: %w", err)
	}
	success := false
	defer func() {
		if err != nil && !success {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				j.State = StateCopying
				if saveErr := SaveJournal(o.Vault, j); saveErr != nil {
					err = fmt.Errorf("%w; cancellation state could not be journaled: %v", err, saveErr)
				} else {
					err = fmt.Errorf("operation canceled; original and owned partial data were preserved; inspect with `midas doctor` or retry `midas resume %s`: %w", o.App, err)
				}
				return
			}
			rb := RollbackOptions{
				App: o.App, Target: o.Target.Name, Vault: o.Vault,
				OperationID: o.OperationID, Context: o.Context,
				Workers: o.Workers,
				Source:  o.Source, Dest: o.Dest, BackupSet: o.BackupSet,
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

	sameVolume, err := sameVolumeCheck(o.Source, parent)
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
		if err := MarkOwnedPath(filepath.Dir(partial), partial, operationID, "move-partial"); err != nil {
			return entry, fmt.Errorf("claim partial path: %w", err)
		}
		if _, cerr := CopyTree(o.Source, partial, CopyOptions{Progress: o.Progress, Context: o.Context, Workers: o.Workers, StageWritable: true}); cerr != nil {
			return entry, fmt.Errorf("copy failed, original untouched: %w", cerr)
		}
		j.State = StateVerifying
		o.Stage(StateVerifying, "verifying copy")
		if err := SaveJournal(o.Vault, j); err != nil {
			return entry, err
		}
		if _, verr := VerifyTreeContext(o.Context, o.Source, partial, o.Verify == "hash", o.VerifyProgress); verr != nil {
			return entry, fmt.Errorf("%w: %v", ErrVerifyFailed, verr)
		}
		// The staged copy was created writable; restore the original modes
		// before TreeSnapshot compares permission bits and before commit.
		if err := applyTreeModes(o.Source, partial); err != nil {
			return entry, fmt.Errorf("apply original modes to staged copy: %w", err)
		}
		j.State = StateRenaming
		o.Stage(StateRenaming, "moving verified copy into place")
		if err := SaveJournal(o.Vault, j); err != nil {
			return entry, err
		}
		if err := os.Rename(partial, o.Dest); err != nil {
			return entry, fmt.Errorf("rename partial copy into place: %w", err)
		}
		if err := ForgetOwnership(filepath.Dir(partial), partial, operationID, "move-partial"); err != nil {
			return entry, fmt.Errorf("verified destination committed but ownership metadata could not be cleared: %w", err)
		}
	}
	if !sameVolume {
		committedSnapshot, err := TreeSnapshot(o.Dest)
		if err != nil || !SameTreeSnapshot(sourceSnapshot, committedSnapshot) {
			if err != nil {
				return entry, fmt.Errorf("could not verify source stability after move: %w", err)
			}
			return entry, fmt.Errorf("source changed during move; the operation was not committed")
		}
	}

	// Steps 6-7: swap the original aside and link it. On the same volume the
	// verified rename above already moved the original, so there is nothing
	// to swap aside. The check is point-in-time: an app that starts after it
	// can still race the rename or unlink below.
	if o.Recheck != nil {
		if err := o.Recheck(); err != nil {
			return entry, fmt.Errorf("app started running during the move: %w", err)
		}
	}
	j.State = StateSwapping
	o.Stage(StateSwapping, "swapping original aside")
	if err := SaveJournal(o.Vault, j); err != nil {
		return entry, err
	}
	if !sameVolume {
		if err := MarkOwnedPath(filepath.Dir(bak), bak, operationID, "move-original"); err != nil {
			return entry, fmt.Errorf("claim original backup path: %w", err)
		}
		if err := os.Rename(o.Source, bak); err != nil {
			_ = ForgetOwnership(filepath.Dir(bak), bak, operationID, "move-original")
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
		OperationID:  operationID,
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
	if PathExists(bak) {
		// Last point-in-time check before the untouched original goes away.
		// If an app started since the swap, keeping .midas-bak would leave
		// its writes in a path nothing reads, so abort into the rollback
		// that restores it instead.
		if o.Recheck != nil {
			if err := o.Recheck(); err != nil {
				return entry, fmt.Errorf("app started running before the untouched original could be removed: %w", err)
			}
		}
		if err := RemoveOwnedPath(filepath.Dir(bak), bak, operationID, "move-original"); err != nil {
			o.Warn("could not remove %s (%v); it is safe to delete by hand or with `midas doctor`", bak, err)
		}
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

func backupID(s *backup.Set) string {
	if s == nil {
		return ""
	}
	return s.ID
}

func moverRealDirectory(path string) bool {
	return platform.IsRealDirectory(path)
}
