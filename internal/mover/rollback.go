package mover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/platform"
)

// RollbackOptions describes a rollback of one app/target.
type RollbackOptions struct {
	App       string
	Target    string
	Vault     string
	Source    string
	Dest      string
	BackupSet *backup.Set
	Manifest  *manifest.Manifest
	Force     bool
	DryRun    bool
	Automatic bool
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
func Rollback(o RollbackOptions) (*RollbackReport, error) {
	if o.Warn == nil {
		o.Warn = func(string, ...any) {}
	}
	rep := &RollbackReport{}
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
	if o.DryRun {
		rep.Notes = append(rep.Notes, "dry run: nothing was changed")
		return rep, nil
	}

	// Scratch partial copies are always safe to remove: they were never the
	// only copy of anything.
	if PathExists(partial) {
		if err := RemoveAllOwned(partial); err != nil {
			return rep, fmt.Errorf("remove partial copy %q: %w", partial, err)
		}
		rep.Notes = append(rep.Notes, "removed the incomplete partial copy")
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
		restored = true

	case prefer == "backup":
		if err := clearOriginal(o.Source, isLink, rep, o.Force); err != nil {
			return rep, err
		}
		if err := restoreFromSet(o.BackupSet, o.Source, o.Progress); err != nil {
			return rep, err
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
		if err := placeFromVault(o.Dest, o.Source, rep, o.Progress); err != nil {
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
		if err := restoreFromSet(o.BackupSet, o.Source, o.Progress); err != nil {
			return rep, err
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
	if err := RemoveJournal(o.Vault, o.App, o.Target); err != nil {
		o.Warn("could not remove journal: %v", err)
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

func placeFromVault(dest, source string, rep *RollbackReport, progress Reporter) error {
	if err := os.Rename(dest, source); err == nil {
		rep.Notes = append(rep.Notes, "the vault copy was moved back (nothing left in the vault)")
		return nil
	}
	// Cross-volume: copy back, then keep the vault copy.
	if _, err := CopyTree(dest, source, CopyOptions{Progress: progress}); err != nil {
		return fmt.Errorf("copy vault data back to %q: %w (vault copy intact at %s)", source, err, dest)
	}
	rep.Notes = append(rep.Notes, "the vault copy was copied back and kept at "+dest)
	return nil
}

func restoreFromSet(s *backup.Set, source string, progress Reporter) error {
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		return err
	}
	if _, err := CopyTree(s.FilesDir(), source, CopyOptions{Progress: progress}); err != nil {
		return fmt.Errorf("restore from backup %s: %w", s.ID, err)
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
