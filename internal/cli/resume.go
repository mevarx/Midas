package cli

import (
	"flag"
	"path/filepath"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/platform"
)

func (c *Ctx) cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	force := fs.Bool("force", false, "resume even if the app appears to be running")
	workers := fs.Int("copy-workers", 1, "bounded copy workers (1-16; default 1)")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas resume <app>")
	}
	if *workers < 1 || *workers > 16 {
		return failf(ExitUsage, "--copy-workers must be between 1 and 16")
	}
	appID := fs.Arg(0)
	vault, err := c.requireVault()
	if err != nil {
		return err
	}
	if !c.G.DryRun {
		release, lerr := c.acquireVaultLock(vault)
		if lerr != nil {
			return lerr
		}
		defer release()
	}
	def, err := c.app(appID)
	if err != nil {
		return err
	}
	if errs := apps.ValidateForHost(def, vault); len(errs) > 0 {
		return failf(ExitUsage, "app definition %s is invalid:\n  %v", def.ID, errs)
	}
	journals, corrupt, err := mover.ScanJournals(vault)
	if err != nil || len(corrupt) > 0 {
		return failf(ExitUnhealthy, "journal state is unreadable; preserve all files and run `midas doctor`")
	}
	var j *mover.Journal
	for _, candidate := range journals {
		if candidate.App != appID {
			continue
		}
		if j != nil {
			return failf(ExitUnhealthy, "multiple operations exist for %s; inspect `midas doctor --json` before recovery", appID)
		}
		j = candidate
	}
	if j == nil {
		return failf(ExitUsage, "no interrupted operation exists for %s", appID)
	}
	if j.Operation != "move" || (j.State != mover.StateCopying && j.State != mover.StateVerifying) || !mover.ValidOperationID(j.OperationID) {
		return failf(ExitUnhealthy, "operation %s is not in a resumable state; preserve its copies and use `midas rollback %s` if the source is valid", j.OperationID, appID)
	}
	resolved, err := resolveTargets(def, vault)
	if err != nil {
		return err
	}
	var target *targetInfo
	for i := range resolved {
		if resolved[i].Name == j.Target {
			target = &resolved[i]
			break
		}
	}
	if target == nil || !platform.SamePath(target.Source, j.Source) || !platform.SamePath(target.Dest, j.Dest) || !platform.SamePath(j.Partial, j.Dest+".midas-partial") {
		return failf(ExitUnhealthy, "journal paths do not match the current app definition; no paths were changed")
	}
	if _, err := mover.TreeSnapshot(j.Source); err != nil || mover.PathExists(j.Dest) {
		return failf(ExitUnhealthy, "source/destination state does not permit a safe resume; preserve all copies")
	}
	if mover.PathExists(j.Bak) || mover.PathExists(j.Bak+".midas-owner.json") {
		return failf(ExitUnhealthy, "original backup path exists; safe resume is not available")
	}
	if mover.PathExists(j.Partial) || mover.PathExists(j.Partial+".midas-owner.json") {
		if err := mover.ValidateOwnedPath(filepath.Dir(j.Partial), j.Partial, j.OperationID, "move-partial"); err != nil {
			return failf(ExitUnhealthy, "partial directory ownership cannot be proven: %v", err)
		}
	}
	if j.BackupID == "" && !j.NoBackup {
		return failf(ExitUnhealthy, "journal does not record a verified backup or explicit no-backup confirmation")
	}
	var set *backup.Set
	if j.BackupID != "" {
		set, err = c.backupManager(vault).Find(j.BackupID)
		if err != nil {
			return failf(ExitUnhealthy, "recorded backup is missing: %v", err)
		}
		if set.State != backup.StateVerified && set.State != backup.StateInUse {
			return failf(ExitUnhealthy, "recorded backup state is %s; resume is unsafe", set.State)
		}
		if err := backup.Verify(set); err != nil {
			return failf(ExitUnhealthy, "recorded backup failed verification: %v", err)
		}
	}
	m, err := manifest.Load(vault)
	if err != nil {
		return failf(ExitUnhealthy, "manifest cannot be reconciled safely: %v", err)
	}
	if _, entry := m.Find(j.App, j.Target); entry != nil {
		return failf(ExitUnhealthy, "manifest already records this target; reconcile it with `midas doctor` instead of resuming")
	}
	if err := c.refuserunning(def, *force); err != nil {
		return err
	}
	plan := map[string]any{"operation_id": j.OperationID, "operation": j.Operation, "state": j.State, "app": j.App, "target": j.Target, "source": j.Source, "destination": j.Dest, "backup_id": j.BackupID, "next_transition": "restart verified copy from the unchanged source"}
	if !c.G.JSON {
		c.UI.Infof("resume plan: operation %s (%s/%s), state %s", j.OperationID, j.App, j.Target, j.State)
		c.UI.Infof("  source: %s", j.Source)
		c.UI.Infof("  destination: %s", j.Dest)
		c.UI.Infof("  backup: %s", j.BackupID)
		c.UI.Infof("  next: restart copy only after verifying ownership and source state")
	}
	if c.G.DryRun {
		if c.G.JSON {
			_ = c.UI.PrintJSON(plan)
		}
		return nil
	}
	bytes, files, err := mover.Measure(target.Source)
	if err != nil {
		return err
	}
	_, err = mover.MoveTarget(mover.MoveOptions{
		App: appID, Target: apps.Target{Name: target.Name}, Resume: true, OperationID: j.OperationID,
		Context: c.Context, Workers: *workers,
		Source: target.Source, Dest: target.Dest, Vault: vault, Verify: c.Cfg.Verify,
		Force: *force, BackupSet: set, NoBackupConfirmed: j.NoBackup,
		SourceBytes: bytes, SourceFiles: files, Manifest: m,
		Warn:  func(f string, a ...any) { c.UI.Warnf(f, a...) },
		Stage: func(stage, detail string) { c.UI.Verbosef("%s: %s", stage, detail) },
	})
	if err != nil {
		return failf(ExitGeneral, "resume failed; the journal and recoverable copies were preserved: %v", err)
	}
	if c.G.JSON {
		plan["resumed"] = true
		_ = c.UI.PrintJSON(plan)
	} else {
		c.UI.Successf("resumed operation %s for %s/%s", j.OperationID, j.App, j.Target)
	}
	return nil
}
