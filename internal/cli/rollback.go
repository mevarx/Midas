package cli

import (
	"flag"

	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/mover"
)

func (c *Ctx) cmdRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	backupID := fs.String("backup", "", "use this backup set")
	source := fs.String("source", "auto", "auto|bak|vault|backup")
	force := fs.Bool("force", false, "roll back even if the app appears to be running")
	workers := fs.Int("copy-workers", 1, "bounded copy workers (1-16; default 1)")
	var targetFlags stringList
	fs.Var(&targetFlags, "target", "roll back only this target (repeatable)")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas rollback <app> [--source auto|bak|vault|backup]")
	}
	if *workers < 1 || *workers > 16 {
		return failf(ExitUsage, "--copy-workers must be between 1 and 16")
	}
	switch *source {
	case "auto", "bak", "vault", "backup":
	default:
		return failf(ExitUsage, "--source must be auto, bak, vault or backup")
	}
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
	def, err := c.app(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.refuserunning(def, *force); err != nil {
		return err
	}
	m, err := manifest.Load(vault)
	if err != nil {
		return err
	}
	mgr := c.backupManager(vault)
	sets, err := mgr.List(def.ID)
	if err != nil {
		return err
	}

	var chosen *backup.Set
	if *backupID != "" {
		for _, s := range sets {
			if s.ID == *backupID {
				chosen = s
				break
			}
		}
		if chosen == nil {
			return failf(ExitUsage, "backup set %q not found for %s", *backupID, def.ID)
		}
	}

	wanted := map[string]bool{}
	for _, t := range targetFlags {
		wanted[t] = true
	}
	targets, err := resolveTargets(def, vault)
	if err != nil {
		return err
	}

	var reports []map[string]any
	found := false
	for _, t := range targets {
		if len(wanted) > 0 && !wanted[t.Name] {
			continue
		}
		j, _ := mover.LoadJournal(vault, def.ID, t.Name)
		_, entry := m.Find(def.ID, t.Name)
		if j == nil && entry == nil && !mover.PathExists(t.Source+".midas-bak") && !mover.PathExists(t.Dest) {
			continue
		}
		found = true
		set := chosen
		if set == nil && entry != nil && entry.Backup.ID != "" {
			for _, s := range sets {
				if s.ID == entry.Backup.ID {
					set = s
					break
				}
			}
		}
		if set == nil {
			for _, s := range sets {
				if s.Target == t.Name {
					set = s
					break
				}
			}
		}

		rep, rerr := mover.Rollback(mover.RollbackOptions{
			App: def.ID, Target: t.Name, Vault: vault,
			Context: c.Context, Workers: *workers,
			Source: t.Source, Dest: t.Dest,
			BackupSet: set, Manifest: m,
			Force: *force, DryRun: c.G.DryRun, Prefer: *source,
			Warn: func(f string, a ...any) { c.UI.Warnf(f, a...) },
		})
		if rerr != nil {
			return failf(ExitGeneral, "%s/%s: %v", def.ID, t.Name, rerr)
		}
		if c.G.JSON {
			reports = append(reports, map[string]any{
				"target": t.Name, "source": rep.SourceUsed, "restored": rep.Restored,
				"kept_aside": rep.KeptAside, "not_covered": rep.NotCovered, "notes": rep.Notes,
			})
			continue
		}
		c.UI.Infof("%s/%s:", def.ID, t.Name)
		c.UI.Infof("  source: %s", rep.SourceUsed)
		if rep.KeptAside != "" {
			c.UI.Infof("  kept aside: %s", rep.KeptAside)
		}
		for _, n := range rep.Notes {
			c.UI.Infof("  %s", n)
		}
		if len(rep.NotCovered) > 0 {
			c.UI.Warnf("not covered by the backup (%d file(s)):", len(rep.NotCovered))
			shown := rep.NotCovered
			if len(shown) > 20 {
				shown = shown[:20]
			}
			for _, p := range shown {
				c.UI.Infof("    %s", p)
			}
		}
		if rep.Restored {
			c.UI.Successf("restored from %s", rep.SourceUsed)
		}
	}
	if !found {
		c.UI.Infof("nothing to roll back for %s", def.ID)
		return nil
	}
	if c.G.JSON {
		return c.UI.PrintJSON(reports)
	}
	if c.G.DryRun {
		c.UI.Infof("dry run: nothing was changed")
	}
	return nil
}
