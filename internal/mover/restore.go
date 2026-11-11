package mover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/platform"
)

// RestoreOptions reverses a completed move.
type RestoreOptions struct {
	App           string
	Target        string
	OperationID   string
	Context       context.Context
	Source        string // original path
	Dest          string // vault path
	Vault         string
	KeepVaultCopy bool
	Verify        string
	Force         bool
	Manifest      *manifest.Manifest
	Progress      Reporter
	Stage         func(stage, detail string)
	Warn          func(format string, a ...any)
}

// Restore copies an app's data back to its original location, verifies it,
// replaces the link, and only then touches the vault copy.
func Restore(o RestoreOptions) error {
	if o.Manifest == nil {
		return fmt.Errorf("restore requires a manifest")
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
	if err := EnsureVaultAppDir(o.Vault, o.App); err != nil {
		return fmt.Errorf("validate vault app directory: %w", err)
	}
	if !PathExists(o.Dest) {
		return fmt.Errorf("vault copy %q is missing; cannot restore %s", o.Dest, o.Source)
	}
	partial := o.Source + ".midas-partial"
	if PathExists(partial) || PathExists(ownerPath(partial)) {
		return fmt.Errorf("partial path %q already exists; preserving it for recovery", partial)
	}
	if JournalExists(o.Vault, o.App, o.Target) {
		return fmt.Errorf("an operation journal already exists for %s/%s; inspect it with `midas doctor`", o.App, o.Target)
	}
	if o.OperationID == "" {
		id, err := NewOperationID()
		if err != nil {
			return err
		}
		o.OperationID = id
	}
	j := &Journal{OperationID: o.OperationID, Operation: "restore", App: o.App, Target: o.Target, State: StateRestoring, Source: o.Source, Dest: o.Dest, Partial: partial}
	if err := SaveJournal(o.Vault, j); err != nil {
		return fmt.Errorf("write restore journal: %w", err)
	}

	sameVolume, err := SameVolume(o.Dest, filepath.Dir(o.Source))
	if err != nil {
		o.Warn("could not determine volume relationship (%v); using the copy path", err)
		sameVolume = false
	}

	restored := false
	if sameVolume && !o.KeepVaultCopy {
		// Rename back is atomic on one volume.
		o.Stage("restoring", "renaming back (same volume)")
		if err := removeLinkIfAny(o.Source); err != nil {
			return err
		}
		if PathExists(o.Source) {
			return fmt.Errorf("original path %q already exists; refusing to overwrite", o.Source)
		}
		if err := os.Rename(o.Dest, o.Source); err != nil {
			return fmt.Errorf("rename vault copy back to %q: %w", o.Source, err)
		}
		restored = true
	} else {
		o.Stage("copying", "copying back from the vault")
		if err := MarkOwnedPath(filepath.Dir(partial), partial, o.OperationID, "restore-partial"); err != nil {
			return fmt.Errorf("claim restore partial path: %w", err)
		}
		if _, err := CopyTree(o.Dest, partial, CopyOptions{Progress: o.Progress, Context: o.Context}); err != nil {
			return fmt.Errorf("copy back failed, link and vault copy untouched; partial data preserved at %s: %w", partial, err)
		}
		j.State = StateVerifying
		if err := SaveJournal(o.Vault, j); err != nil {
			return fmt.Errorf("write restore verification state: %w", err)
		}
		o.Stage("verifying", "verifying copied data")
		if _, err := VerifyTree(o.Dest, partial, o.Verify == "hash"); err != nil {
			return fmt.Errorf("%w: %v; link and vault copy untouched", ErrVerifyFailed, err)
		}
		j.State = StateRenaming
		if err := SaveJournal(o.Vault, j); err != nil {
			return fmt.Errorf("write restore commit state: %w", err)
		}
		if err := removeLinkIfAny(o.Source); err != nil {
			return err
		}
		if PathExists(o.Source) {
			return fmt.Errorf("original path %q already exists; refusing to overwrite", o.Source)
		}
		if err := os.Rename(partial, o.Source); err != nil {
			return fmt.Errorf("move verified copy into place: %w", err)
		}
		if err := ForgetOwnership(filepath.Dir(partial), partial, o.OperationID, "restore-partial"); err != nil {
			return fmt.Errorf("restored data is in place but ownership metadata could not be cleared: %w", err)
		}
		restored = true
	}

	if !restored {
		return fmt.Errorf("restore did not complete")
	}

	o.Manifest.Remove(o.App, o.Target)
	if err := o.Manifest.Save(o.Vault); err != nil {
		return fmt.Errorf("data is restored, but the manifest could not be updated: %w", err)
	}
	if !o.KeepVaultCopy {
		if err := MarkExistingOwnedPath(filepath.Dir(o.Dest), o.Dest, o.OperationID, "restore-vault-copy"); err != nil {
			o.Warn("data is restored, but the vault copy at %s could not be claimed for cleanup: %v", o.Dest, err)
		} else if err := RemoveOwnedPath(filepath.Dir(o.Dest), o.Dest, o.OperationID, "restore-vault-copy"); err != nil {
			o.Warn("data is restored, but the vault copy at %s could not be removed: %v", o.Dest, err)
		}
	} else {
		o.Stage("done", "vault copy kept at "+o.Dest)
	}
	j.State = StateDone
	if err := SaveJournal(o.Vault, j); err != nil {
		return fmt.Errorf("restore completed but final journal state could not be saved: %w", err)
	}
	if err := RemoveJournal(o.Vault, o.App, o.Target); err != nil {
		return fmt.Errorf("restore completed but journal could not be removed: %w", err)
	}
	return nil
}

func removeLinkIfAny(p string) error {
	_, isLink, err := platform.Current().ReadLink(p)
	if err != nil {
		return err
	}
	if !isLink {
		return nil
	}
	return platform.Current().RemoveLink(p)
}
