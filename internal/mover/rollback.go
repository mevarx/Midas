package mover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
)

// RollbackOptions describes a rollback of one app/target.
type RollbackOptions struct {
	App         string
	Target      string
	OperationID string
	Context     context.Context
	Workers     int
	Vault       string
	Source      string
	Dest        string
	BackupSet   *backup.Set
	Manifest    *manifest.Manifest
	Force       bool
	DryRun      bool
	Automatic   bool
	// Prefer forces a source: auto, bak, vault or backup.
	Prefer   string
	Warn     func(format string, a ...any)
	Progress Reporter
}

// RollbackReport explains what happened.
type RollbackReport struct {
	SourceUsed string   // human description of the data source
	Restored   bool     // original tree was rebuilt
	KeptAside  string   // displaced folder, if the original was a real directory
	NotCovered []string // files an essential backup could not restore
	Notes      []string
}

// Rollback restores an app target to its pre-move state, preferring the
// untouched original, then the vault copy, then the backup set.
func Rollback(o RollbackOptions) (rep *RollbackReport, returnErr error) {
	if o.Warn == nil {
		o.Warn = func(string, ...any) {}
	}
	if err := EnsureVaultAppDir(o.Vault, o.App); err != nil {
		return nil, fmt.Errorf("validate vault app directory: %w", err)
	}
	rep = &RollbackReport{}
	bak := o.Source + ".midas-bak"
	partial := o.Dest + ".midas-partial"

	bakExists := PathExists(bak)
	linkTarget, isLink, _ := platform.Current().ReadLink(o.Source)
	srcExists := PathExists(o.Source)
	destExists := PathExists(o.Dest)

	prefer := o.Prefer
	if prefer == "" {
		prefer = "auto"
	}
	switch prefer {
	case "auto":
	case "bak":
		if !bakExists {
			return rep, fmt.Errorf("--source=bak requested but %q does not exist", bak)
		}
	case "vault":
		if !destExists {
			return rep, fmt.Errorf("--source=vault requested but %q does not exist", o.Dest)
		}
	case "backup":
		if o.BackupSet == nil || !PathExists(o.BackupSet.FilesDir()) {
			return rep, fmt.Errorf("--source=backup requested but no usable backup set was found")
		}
	default:
		return rep, fmt.Errorf("unknown rollback source %q", prefer)
	}

	// Choose the source and describe the plan.
	switch {
	case prefer == "bak" || (prefer == "auto" && bakExists):
		rep.SourceUsed = "the untouched original (.midas-bak)"
	case prefer == "backup":
		rep.SourceUsed = "the backup set " + o.BackupSet.ID
	case prefer == "vault" || (prefer == "auto" && (isLink || !srcExists) && destExists):
		rep.SourceUsed = "the verified vault copy"
	case prefer == "auto" && srcExists && !isLink:
		rep.SourceUsed = "the original (still in place; nothing to restore)"
	case o.BackupSet != nil && PathExists(o.BackupSet.FilesDir()):
		rep.SourceUsed = "the backup set " + o.BackupSet.ID
	default:
		return rep, fmt.Errorf("no usable rollback source: %s does not exist, no .midas-bak, no vault copy and no backup set", o.Source)
	}
	if isLink && destExists && !bakExists {
		rep.Notes = append(rep.Notes, fmt.Sprintf("the link at %s points to %s", o.Source, linkTarget))
	}
	if o.BackupSet != nil && o.BackupSet.Mode == backup.ModeEssential {
		for _, s := range o.BackupSet.Skipped {
			rep.NotCovered = append(rep.NotCovered, s.Path+" ("+s.Reason+")")
		}
		if len(rep.NotCovered) > 0 {
			rep.Notes = append(rep.Notes, "an essential backup does not contain every file; the list above was not covered")
		}
	}
	if o.BackupSet != nil && rep.SourceUsed == "the backup set "+o.BackupSet.ID {
		if err := backup.Verify(o.BackupSet); err != nil {
			return rep, fmt.Errorf("refusing to restore from an invalid backup set: %w", err)
		}
	}
	if o.DryRun {
		rep.Notes = append(rep.Notes, "dry run: nothing was changed")
		return rep, nil
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	rollbackPartial := o.Source + ".midas-partial"
	journal, err := LoadJournal(o.Vault, o.App, o.Target)
	if err != nil {
		return rep, fmt.Errorf("inspect operation journal before rollback: %w", err)
	}
	parentOperationID := ""
	previousOperationID := ""
	if journal != nil {
		if !platform.SamePath(journal.Source, o.Source) || !platform.SamePath(journal.Dest, o.Dest) {
			return rep, errors.New("journal paths do not match the requested rollback; preserve all copies")
		}
		previousOperationID = journal.OperationID
		parentOperationID = journal.OperationID
		if journal.Operation == "rollback" && journal.ParentOperationID != "" {
			parentOperationID = journal.ParentOperationID
		}
	}
	operationID := o.OperationID
	if operationID == "" || operationID == parentOperationID {
		operationID, err = NewOperationID()
		if err != nil {
			return rep, err
		}
	}
	freshJournal := journal == nil
	if journal == nil {
		journal = &Journal{App: o.App, Target: o.Target}
	}
	if PathExists(rollbackPartial) || PathExists(ownerPath(rollbackPartial)) {
		if journal.Operation != "rollback" || !validOperationID(previousOperationID) || (srcExists && !isLink) || (!destExists && o.BackupSet == nil) {
			return rep, fmt.Errorf("partial restore path %q cannot be matched to a recoverable rollback; preserving it", rollbackPartial)
		}
		if err := ValidateOwnedPath(filepath.Dir(rollbackPartial), rollbackPartial, previousOperationID, "rollback-partial"); err != nil {
			return rep, fmt.Errorf("partial restore ownership cannot be proven; preserving %s: %w", rollbackPartial, err)
		}
		if err := RemoveOwnedPath(filepath.Dir(rollbackPartial), rollbackPartial, previousOperationID, "rollback-partial"); err != nil {
			return rep, fmt.Errorf("cannot restart interrupted rollback from %s: %w", rollbackPartial, err)
		}
	}
	journal.ParentOperationID = parentOperationID
	journal.OperationID = operationID
	journal.Operation = "rollback"
	journal.State = StateRollingBack
	journal.Source = o.Source
	journal.Dest = o.Dest
	journal.Partial = rollbackPartial
	journal.Bak = bak
	if o.BackupSet != nil {
		journal.BackupID = o.BackupSet.ID
	}
	if freshJournal {
		if err := CreateJournal(o.Vault, journal); err != nil {
			return rep, fmt.Errorf("write rollback journal: %w", err)
		}
	} else if err := SaveJournal(o.Vault, journal); err != nil {
		return rep, fmt.Errorf("write rollback journal: %w", err)
	}
	journalActive := true
	defer func() {
		if journalActive && returnErr != nil {
			if errors.Is(returnErr, context.Canceled) || errors.Is(returnErr, context.DeadlineExceeded) {
				journal.State = StateNeedsRecovery
			}
			if saveErr := SaveJournal(o.Vault, journal); saveErr != nil {
				returnErr = fmt.Errorf("%w; rollback state could not be journaled: %v", returnErr, saveErr)
			}
		}
	}()
	if err := o.Context.Err(); err != nil {
		return rep, err
	}

	// Unknown partials may contain the only recoverable copy; preserve them.
	if PathExists(partial) {
		rep.Notes = append(rep.Notes, "preserved the partial copy at "+partial+"; inspect it before cleanup")
	}

	restored := false
	switch {
	case prefer == "bak" || (prefer == "auto" && bakExists):
		if err := clearOriginal(o.Source, isLink, rep, o.Force); err != nil {
			return rep, err
		}
		if err := os.Rename(bak, o.Source); err != nil {
			return rep, fmt.Errorf("restore %q from .midas-bak: %w (the original is intact at %s)", o.Source, err, bak)
		}
		if parentOperationID != "" {
			_ = ForgetOwnership(filepath.Dir(bak), bak, parentOperationID, "move-original")
		}
		restored = true

	case prefer == "backup":
		if err := clearOriginal(o.Source, isLink, rep, o.Force); err != nil {
			return rep, err
		}
		if err := restoreFromSet(o.BackupSet, o.Source, o.Progress, o.Context, o.Workers, operationID, func(format string, a ...any) {
			rep.Notes = append(rep.Notes, fmt.Sprintf(format, a...))
		}); err != nil {
			return rep, err
		}
		if len(o.BackupSet.Dirs) == 0 {
			rep.Notes = append(rep.Notes, "the backup set predates directory metadata; directory modes and timestamps were not recorded and may differ")
		}
		if problems := verifyRestored(o.Source, o.BackupSet); len(problems) > 0 {
			return rep, fmt.Errorf("restored files failed verification:\n  %s", strings.Join(problems, "\n  "))
		}
		restored = true

	case prefer == "vault" || isLink || (!srcExists && destExists):
		if isLink {
			if err := platform.Current().RemoveLink(o.Source); err != nil {
				return rep, fmt.Errorf("remove link %q: %w", o.Source, err)
			}
		} else if srcExists {
			if err := clearOriginal(o.Source, false, rep, o.Force); err != nil {
				return rep, err
			}
		}
		if err := placeFromVault(o.Dest, o.Source, rep, o.Progress, o.Context, o.Workers, operationID); err != nil {
			return rep, err
		}
		restored = true

	case srcExists && !isLink:
		// Data never left the original path.
		restored = true

	default:
		if o.BackupSet == nil {
			return rep, errors.New("no rollback source available")
		}
		if err := restoreFromSet(o.BackupSet, o.Source, o.Progress, o.Context, o.Workers, operationID, func(format string, a ...any) {
			rep.Notes = append(rep.Notes, fmt.Sprintf(format, a...))
		}); err != nil {
			return rep, err
		}
		if len(o.BackupSet.Dirs) == 0 {
			rep.Notes = append(rep.Notes, "the backup set predates directory metadata; directory modes and timestamps were not recorded and may differ")
		}
		if problems := verifyRestored(o.Source, o.BackupSet); len(problems) > 0 {
			return rep, fmt.Errorf("restored files failed verification:\n  %s", strings.Join(problems, "\n  "))
		}
		restored = true
	}

	rep.Restored = restored

	// Manifest and journal bookkeeping.
	if o.Manifest != nil {
		o.Manifest.Remove(o.App, o.Target)
		if err := o.Manifest.Save(o.Vault); err != nil {
			o.Warn("could not update manifest: %v", err)
		}
	}
	if o.BackupSet != nil {
		switch o.BackupSet.State {
		case backup.StateCreating, backup.StateVerified, backup.StateInUse, backup.StateComplete:
			if err := o.BackupSet.MarkRolledBack(); err != nil {
				o.Warn("could not mark backup %s as rolled back: %v", o.BackupSet.ID, err)
			}
		}
	}
	journal.State = StateRolledBack
	if err := SaveJournal(o.Vault, journal); err != nil {
		return rep, fmt.Errorf("rollback completed but final journal state could not be saved: %w", err)
	}
	if err := RemoveJournal(o.Vault, o.App, o.Target); err != nil {
		o.Warn("could not remove journal: %v", err)
	} else {
		journalActive = false
	}
	rep.Notes = append(rep.Notes,
		"the backup set and any vault copy were kept; Midas never deletes them here")
	return rep, nil
}

func clearOriginal(source string, isLink bool, rep *RollbackReport, force bool) error {
	if !PathExists(source) {
		return nil
	}
	if isLink {
		if err := platform.Current().RemoveLink(source); err != nil {
			return fmt.Errorf("remove link %q: %w", source, err)
		}
		return nil
	}
	// A real directory at the original path is partial work: move it aside,
	// never delete it.
	aside := source + ".midas-rollback-" + time.Now().Format("20060102-150405")
	if err := os.Rename(source, aside); err != nil {
		return fmt.Errorf("move partial folder aside to %q: %w", aside, err)
	}
	rep.KeptAside = aside
	rep.Notes = append(rep.Notes, "the folder found at the original path was kept at "+aside)
	return nil
}

func placeFromVault(dest, source string, rep *RollbackReport, progress Reporter, ctx context.Context, workers int, operationID string) error {
	if err := os.Rename(dest, source); err == nil {
		rep.Notes = append(rep.Notes, "the vault copy was moved back (nothing left in the vault)")
		return nil
	}
	// Cross-volume: stage and verify before exposing the restored tree.
	partial := source + ".midas-partial"
	if err := MarkOwnedPath(filepath.Dir(partial), partial, operationID, "rollback-partial"); err != nil {
		return err
	}
	if _, err := CopyTree(dest, partial, CopyOptions{Progress: progress, Context: ctx, Workers: workers, StageWritable: true}); err != nil {
		return fmt.Errorf("copy vault data to recovery path %q: %w (vault copy intact at %s)", partial, err, dest)
	}
	if _, err := VerifyTree(dest, partial, true); err != nil {
		return fmt.Errorf("verify staged rollback copy %q: %w (vault copy intact at %s)", partial, err, dest)
	}
	if err := applyTreeModes(dest, partial); err != nil {
		return fmt.Errorf("apply original modes to staged rollback copy %q: %w (vault copy intact at %s)", partial, err, dest)
	}
	if err := os.Rename(partial, source); err != nil {
		return fmt.Errorf("commit verified rollback copy to %q: %w", source, err)
	}
	// The restored tree is committed at source; a marker cleanup failure is
	// metadata-only and must not fail the rollback.
	if err := forgetOwnership(filepath.Dir(partial), partial, operationID, "rollback-partial"); err != nil {
		rep.Notes = append(rep.Notes, fmt.Sprintf("the rollback copy was committed, but ownership metadata at %s could not be cleared (%v); the data is safe and `midas doctor` can reconcile the stale marker", ownerPath(partial), err))
	}
	rep.Notes = append(rep.Notes, "the vault copy was copied back and kept at "+dest)
	return nil
}

func restoreFromSet(s *backup.Set, source string, progress Reporter, ctx context.Context, workers int, operationID string, warn func(format string, a ...any)) error {
	if warn == nil {
		warn = func(string, ...any) {}
	}
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		return err
	}
	partial := source + ".midas-partial"
	if err := MarkOwnedPath(filepath.Dir(partial), partial, operationID, "rollback-partial"); err != nil {
		return err
	}
	if _, err := CopyTree(s.FilesDir(), partial, CopyOptions{Progress: progress, Context: ctx, Workers: workers, StageWritable: true}); err != nil {
		return fmt.Errorf("restore from backup %s: %w", s.ID, err)
	}
	if _, err := VerifyTree(s.FilesDir(), partial, true); err != nil {
		return fmt.Errorf("verify backup restore staging path %s: %w", partial, err)
	}
	if err := os.Rename(partial, source); err != nil {
		return err
	}
	// The restored tree is committed at source; a marker cleanup failure is
	// metadata-only and must not fail the rollback.
	if err := forgetOwnership(filepath.Dir(partial), partial, operationID, "rollback-partial"); err != nil {
		warn("the restored copy was committed, but ownership metadata at %s could not be cleared (%v); the data is safe and `midas doctor` can reconcile the stale marker", ownerPath(partial), err)
	}
	// The set tree is chmod'ed read-only for protection; the recorded
	// per-file modes from BACKUP.json are the ones the original data had.
	for _, f := range s.Files {
		if f.Link != "" {
			continue
		}
		if err := os.Chmod(filepath.Join(source, f.Path), os.FileMode(f.Mode)); err != nil {
			return fmt.Errorf("restore file mode for %q: %w", f.Path, err)
		}
	}
	if len(s.Dirs) > 0 {
		dirs := make([]backup.Dir, len(s.Dirs))
		copy(dirs, s.Dirs)
		// Bottom-up (reverse lexical order): children before parents and the
		// root last, so a read-only child directory cannot block changes.
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].Path > dirs[j].Path })
		for _, d := range dirs {
			p := source
			if d.Path != "." {
				p = filepath.Join(source, d.Path)
			}
			if err := os.Chmod(p, os.FileMode(d.Mode)); err != nil {
				return fmt.Errorf("restore directory mode for %q: %w", d.Path, err)
			}
			if !d.MTime.IsZero() {
				if err := os.Chtimes(p, d.MTime, d.MTime); err != nil {
					return fmt.Errorf("restore directory timestamp for %q: %w", d.Path, err)
				}
			}
		}
	}
	return nil
}

func verifyRestored(source string, s *backup.Set) []string {
	var problems []string
	for _, f := range s.Files {
		p := filepath.Join(source, f.Path)
		if f.Link != "" {
			t, isLink, err := platform.Current().ReadLink(p)
			if err != nil || !isLink || t != f.Link {
				problems = append(problems, f.Path+": link missing")
			}
			continue
		}
		fi, err := os.Lstat(p)
		if err != nil {
			problems = append(problems, f.Path+": missing")
			continue
		}
		if fi.Size() != f.Size {
			problems = append(problems, f.Path+": size mismatch")
			continue
		}
		if f.SHA256 != "" {
			got, err := hashFile(p)
			if err != nil || got != f.SHA256 {
				problems = append(problems, f.Path+": hash mismatch")
			}
		}
	}
	return problems
}
