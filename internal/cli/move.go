package cli

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/mover"
	"github.com/midas-cli/midas/internal/platform"
	"github.com/midas-cli/midas/internal/snapshot"
	"github.com/midas-cli/midas/internal/ui"
)

// stringList collects a repeatable flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func (c *Ctx) cmdMove(args []string) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	var targetFlags stringList
	fs.Var(&targetFlags, "target", "move only this target (repeatable)")
	backupMode := fs.String("backup", "", "backup mode: full|essential|none (default from config)")
	backupDirF := fs.String("backup-dir", "", "backup root for this move")
	restorePointF := fs.String("restore-point", "", "auto|require|skip")
	force := fs.Bool("force", false, "move even if the app appears to be running")
	understood := fs.Bool("i-understand-no-backup", false, "required with --backup=none")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas move <app> [--target <name>]")
	}
	appID := fs.Arg(0)
	vault, err := c.requireVault()
	if err != nil {
		return err
	}
	def, err := c.app(appID)
	if err != nil {
		return err
	}
	if errs := apps.ValidateForHost(def, vault); len(errs) > 0 {
		return failf(ExitUsage, "app definition %s is invalid:\n  %v", def.ID, errs)
	}

	mode := *backupMode
	if mode == "" {
		mode = c.Cfg.Backup
	}
	switch mode {
	case backup.ModeFull, backup.ModeEssential, backup.ModeNone:
	default:
		return failf(ExitUsage, "--backup must be full, essential or none, got %q", mode)
	}
	rpPolicy := *restorePointF
	if rpPolicy == "" {
		rpPolicy = c.Cfg.RestorePoint
	}
	backupRoot := *backupDirF
	if backupRoot == "" {
		backupRoot = c.Cfg.EffectiveBackupDir(vault)
	}

	// Select targets.
	resolved, err := resolveTargets(def, vault)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, n := range targetFlags {
		if _, ok := def.TargetByName(n); !ok {
			return failf(ExitUsage, "app %s has no target %q (targets: %s)", def.ID, n, strings.Join(def.TargetNames(), ", "))
		}
		wanted[n] = true
	}
	var todo []targetInfo
	for _, t := range resolved {
		if len(wanted) > 0 && !wanted[t.Name] {
			continue
		}
		switch {
		case t.Broken:
			c.UI.Warnf("%s/%s: link at %s is broken; run `midas doctor` instead of moving", def.ID, t.Name, t.Source)
		case t.IsLink:
			c.UI.Infof("%s/%s: already moved (link at %s)", def.ID, t.Name, t.Source)
		case !t.Exists:
			c.UI.Infof("%s/%s: %s does not exist; skipping", def.ID, t.Name, t.Source)
		case mover.PathExists(t.Dest):
			c.UI.Warnf("%s/%s: destination %s already exists; skipping", def.ID, t.Name, t.Dest)
		default:
			if mover.JournalExists(vault, def.ID, t.Name) {
				return failf(ExitGeneral, "an incomplete move journal exists for %s/%s; run `midas doctor` to resume or roll back first", def.ID, t.Name)
			}
			if pathOverlaps(t.Source, vault) {
				return failf(ExitUsage, "refusing to move %q: the vault %q overlaps it (a vault inside a target would recurse)", t.Source, vault)
			}
			todo = append(todo, t)
		}
	}
	if len(todo) == 0 {
		c.UI.Infof("nothing to move for %s", def.ID)
		return nil
	}

	if err := c.refuserunning(def, *force); err != nil {
		return err
	}
	if mode != backup.ModeNone {
		if err := c.ensureBackupLocation(backupRoot); err != nil {
			return failf(ExitBackupFailed, "%v", err)
		}
	}

	// Pre-flight: sizes and space.
	type planItem struct {
		target    targetInfo
		bytes     uint64
		files     int
		backupEst backup.Estimate
	}
	items := make([]planItem, 0, len(todo))
	var sourceBytes, backupBytes uint64
	rule := def.EffectiveBackupRule()
	for _, t := range todo {
		b, f, err := mover.Measure(t.Source)
		if err != nil {
			return err
		}
		it := planItem{target: t, bytes: b, files: f}
		if mode == backup.ModeFull {
			it.backupEst = backup.Estimate{Bytes: int64(b), Files: f}
		} else if mode == backup.ModeEssential {
			est, err := backup.EstimateBackup(t.Source, mode, rule)
			if err != nil {
				return err
			}
			it.backupEst = est
		}
		items = append(items, it)
		sourceBytes += b
		if it.backupEst.Bytes > 0 {
			backupBytes += uint64(it.backupEst.Bytes)
		}
	}

	vaultFree, err := platform.Current().FreeSpace(vault)
	if err != nil {
		return failf(ExitVaultUnreach, "cannot read free space on the vault: %v", err)
	}
	backupFree, err := platform.Current().FreeSpace(backupRoot)
	if err != nil {
		if mode != backup.ModeNone {
			return failf(ExitBackupFailed, "cannot read free space on the backup location: %v", err)
		}
		backupFree = 0
	}
	sameVol, _ := mover.SameVolume(vault, backupRoot)
	needVault := sourceBytes + sourceBytes/10
	needBackup := backupBytes + backupBytes/10
	if mode == backup.ModeNone {
		needBackup = 0
	}
	var missing []string
	if needVault > vaultFree {
		missing = append(missing, fmt.Sprintf("vault needs %s, has %s", ui.FormatBytes(needVault), ui.FormatBytes(vaultFree)))
	}
	if needBackup > 0 && sameVol {
		total := needVault + needBackup
		if total > vaultFree {
			missing = append(missing, fmt.Sprintf("vault and backup share a volume and together need %s, available %s",
				ui.FormatBytes(total), ui.FormatBytes(vaultFree)))
		}
	} else if needBackup > backupFree {
		missing = append(missing, fmt.Sprintf("backup location needs %s, has %s", ui.FormatBytes(needBackup), ui.FormatBytes(backupFree)))
	}
	if len(missing) > 0 {
		return failf(ExitNoSpace, "not enough space: %s.\nUse --backup-dir on another drive, or deliberately choose --backup=essential (never silently downgraded).", strings.Join(missing, "; "))
	}

	// Print the plan.
	c.UI.Infof("Move plan for %s (%s)", def.Name, def.ID)
	for _, it := range items {
		c.UI.Infof("  %s:", it.target.Name)
		c.UI.Infof("    from %s", it.target.Source)
		c.UI.Infof("    to   %s", it.target.Dest)
		c.UI.Infof("    %s in %d files", ui.FormatBytes(it.bytes), it.files)
		switch mode {
		case backup.ModeNone:
			c.UI.Infof("    backup: NONE (explicitly confirmed, not recommended)")
		default:
			c.UI.Infof("    backup: %s, about %s in %d files, under %s",
				mode, ui.FormatBytes(uint64(it.backupEst.Bytes)), it.backupEst.Files, filepath.Join(backupRoot, def.ID))
			if it.backupEst.SkippedFiles > 0 {
				c.UI.Infof("    not backed up: %d files, %s", it.backupEst.SkippedFiles, ui.FormatBytes(uint64(it.backupEst.SkippedBytes)))
			}
		}
	}
	if sameVol && mode != backup.ModeNone {
		c.UI.Warnf("the backup directory is on the same volume as the vault: one drive failure would destroy both. Use --backup-dir on a different drive.")
		if mode == backup.ModeFull {
			c.UI.Infof("  combined space needed on that volume: %s for the vault copy plus %s for the backup",
				ui.FormatBytes(needVault), ui.FormatBytes(needBackup))
		}
	}
	c.UI.Infof("  restore point: %s", rpPolicy)
	c.UI.Infof("  retention: %s", c.Cfg.BackupRetention)

	if c.G.DryRun {
		c.UI.Infof("dry run: nothing was changed")
		return nil
	}

	if mode == backup.ModeNone {
		if !*understood {
			return failf(ExitUsage, "--backup=none also needs --i-understand-no-backup")
		}
		ok, err := c.UI.ConfirmTyped("No backup will be taken. This cannot be undone if something goes wrong.", "no backup")
		if err != nil {
			return err
		}
		if !ok {
			return failf(ExitUsage, "confirmation did not match; nothing was changed")
		}
	}

	ok, err := c.confirm(fmt.Sprintf("Move %d target(s) for %s?", len(items), def.ID), true)
	if err != nil {
		return err
	}
	if !ok {
		return failf(ExitGeneral, "cancelled; nothing was changed")
	}

	// The safety net: one restore-point attempt per move.
	rp, err := snapshot.Create(rpPolicy)
	if err != nil {
		return failf(ExitBackupFailed, "%v", err)
	}
	if rp.Attempted && !rp.Created {
		c.UI.Warnf("restore point not created: %s", rp.Reason)
		c.UI.Warnf("restore points mainly protect system files, not your profile; the verified file backup is the real safety net")
	}

	m, err := manifest.Load(vault)
	if err != nil {
		return err
	}
	mgr := c.backupManager(vault)
	var createdSets []*backup.Set

	for _, it := range items {
		appID := def.ID
		var set *backup.Set
		if mode != backup.ModeNone {
			estBytes := it.backupEst.Bytes
			progress := c.UI.NewProgress("backup "+it.target.Name, estBytes)
			set, err = mgr.Create(backup.CreateOptions{
				App:          appID,
				Target:       it.target.Name,
				OriginalPath: it.target.Source,
				Mode:         mode,
				Rule:         rule,
				HashVerify:   c.Cfg.Verify == "hash",
				Progress:     progress,
			}, rp)
			progress.Done()
			if err != nil {
				return failf(ExitBackupFailed, "%v", err)
			}
			createdSets = append(createdSets, set)
			c.UI.Infof("backup set %s verified at %s", set.ID, set.Dir)
			if len(set.Skipped) > 0 {
				c.UI.Infof("  %d file(s) not covered by this backup (see `midas backup show %s`)", len(set.Skipped), set.ID)
			}
		}

		progress := c.UI.NewProgress("move "+it.target.Name, int64(it.bytes))
		if sameVolumeMove, verr := mover.SameVolume(it.target.Source, vault); verr == nil && sameVolumeMove {
			progress = nil
		}
		var progReporter mover.Reporter
		if progress != nil {
			progReporter = progress
		}
		entry, merr := mover.MoveTarget(mover.MoveOptions{
			App:               appID,
			Target:            apps.Target{Name: it.target.Name},
			Source:            it.target.Source,
			Dest:              it.target.Dest,
			Vault:             vault,
			Verify:            c.Cfg.Verify,
			Force:             *force,
			BackupSet:         set,
			NoBackupConfirmed: mode == backup.ModeNone,
			SourceBytes:       it.bytes,
			SourceFiles:       it.files,
			Manifest:          m,
			Progress:          progReporter,
			Stage:             func(stage, detail string) { c.UI.Verbosef("%s: %s", it.target.Name, detail) },
			Warn:              func(f string, a ...any) { c.UI.Warnf(f, a...) },
		})
		if progress != nil {
			progress.Done()
		}
		if merr != nil {
			c.Logf("move failed app=%s target=%s: %v", appID, it.target.Name, merr)
			if errorsIsVerify(merr) {
				return failf(ExitVerifyFailed, "%v", merr)
			}
			return failf(ExitGeneral, "%v", merr)
		}
		c.UI.Successf("%s/%s moved: %s -> %s", def.ID, it.target.Name, entry.OriginalPath, entry.VaultPath)
	}

	c.Logf("move complete app=%s mode=%s targets=%d", def.ID, mode, len(items))
	if c.G.JSON {
		return c.UI.PrintJSON(map[string]any{
			"app": def.ID, "mode": mode, "targets": len(items),
			"backup_sets": setIDs(createdSets),
		})
	}

	// Retention.
	if err := c.applyRetention(vault, createdSets); err != nil {
		c.UI.Warnf("%v", err)
	}
	if len(createdSets) > 0 {
		c.UI.Infof("backups are kept until you delete them: `midas backup delete <id>`")
	}
	return nil
}

func setIDs(sets []*backup.Set) []string {
	out := make([]string, 0, len(sets))
	for _, s := range sets {
		out = append(out, s.ID)
	}
	return out
}

func errorsIsVerify(err error) bool {
	return errors.Is(err, mover.ErrVerifyFailed)
}

// applyRetention implements keep / delete-on-success / <N>d with the
// protected Delete path. This run only creates sets, so <N>d does nothing
// here; the sweep runs from list/status.
func (c *Ctx) applyRetention(vault string, sets []*backup.Set) error {
	if len(sets) == 0 {
		return nil
	}
	switch c.Cfg.BackupRetention {
	case "delete-on-success":
		for _, s := range sets {
			if err := c.deleteBackupWithGuard(vault, s, true, nil); err != nil {
				return fmt.Errorf("retention: %v", err)
			}
		}
	case "keep":
		if c.G.Yes {
			return nil
		}
		for _, s := range sets {
			ok, err := c.UI.Confirm(fmt.Sprintf("Delete backup %s now?", s.ID), false)
			if err != nil {
				return err
			}
			if ok {
				if err := c.deleteBackupWithGuard(vault, s, true, nil); err != nil {
					return fmt.Errorf("backup %s: %v", s.ID, err)
				}
			}
		}
	}
	return nil
}
