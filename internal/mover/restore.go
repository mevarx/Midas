package mover

import (
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
	if o.Warn == nil {
		o.Warn = func(string, ...any) {}
	}
	if !PathExists(o.Dest) {
		return fmt.Errorf("vault copy %q is missing; cannot restore %s", o.Dest, o.Source)
	}
	partial := o.Source + ".midas-partial"
	if PathExists(partial) {
		if err := RemoveAllOwned(partial); err != nil {
			return err
		}
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
		if _, err := CopyTree(o.Dest, partial, CopyOptions{Progress: o.Progress}); err != nil {
			return fmt.Errorf("copy back failed, link and vault copy untouched: %w", err)
		}
		o.Stage("verifying", "verifying copied data")
		if _, err := VerifyTree(o.Dest, partial, o.Verify == "hash"); err != nil {
			_ = RemoveAllOwned(partial)
			return fmt.Errorf("%w: %v; link and vault copy untouched", ErrVerifyFailed, err)
		}
		if err := removeLinkIfAny(o.Source); err != nil {
			_ = RemoveAllOwned(partial)
			return err
		}
		if PathExists(o.Source) {
			_ = RemoveAllOwned(partial)
			return fmt.Errorf("original path %q already exists; refusing to overwrite", o.Source)
		}
		if err := os.Rename(partial, o.Source); err != nil {
			_ = RemoveAllOwned(partial)
			return fmt.Errorf("move verified copy into place: %w", err)
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
		if err := RemoveAllOwned(o.Dest); err != nil {
			o.Warn("data is restored, but the vault copy at %s could not be removed: %v", o.Dest, err)
		}
	} else {
		o.Stage("done", "vault copy kept at "+o.Dest)
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
