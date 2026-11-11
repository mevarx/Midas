package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/mover"
	"github.com/midas-cli/midas/internal/platform"
)

func (c *Ctx) cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	repair := fs.Bool("repair", false, "apply the safe repairs without asking")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	vault, err := c.Vault()
	if err != nil {
		return err
	}
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if fi, err := os.Stat(vault); err != nil || !fi.IsDir() {
		return failf(ExitUnhealthy, "vault %q is not reachable (is the drive mounted?)", vault)
	}

	m, merr := manifest.Load(vault)
	if merr != nil {
		c.UI.Warnf("manifest is unreadable: %v", merr)
		if rebuilt, rerr := rebuildManifest(vault); rerr == nil && len(rebuilt.Entries) > 0 {
			m = rebuilt
			if !c.G.DryRun {
				_ = m.Save(vault)
			}
			c.UI.Successf("rebuilt manifest with %d entr(ies) from links that point into the vault", len(m.Entries))
		} else {
			return failf(ExitUnhealthy, "manifest is corrupt and could not be rebuilt: %v", merr)
		}
	}

	// Moved targets.
	for i := range m.Entries {
		e := &m.Entries[i]
		ok, detail := mover.HealthCheck(e.OriginalPath, e.VaultPath)
		if ok {
			continue
		}
		srcExists := mover.PathExists(e.OriginalPath)
		destExists := mover.DirExists(e.VaultPath)
		switch {
		case destExists && !srcExists:
			add("%s/%s: link missing at %s (vault copy intact)", e.App, e.Target, e.OriginalPath)
			if c.shouldRepair(*repair) {
				if c.G.DryRun {
					c.UI.Infof("dry run: would recreate link %s -> %s", e.OriginalPath, e.VaultPath)
				} else if err := platform.Current().CreateLink(e.OriginalPath, e.VaultPath); err != nil {
					c.UI.Errorf("repair failed: %v", err)
				} else {
					c.UI.Successf("recreated link %s -> %s", e.OriginalPath, e.VaultPath)
					problems = problems[:len(problems)-1]
				}
			}
		case destExists && srcExists:
			add("%s/%s: %s is a real folder again (an app update may have replaced the link); the vault copy is at %s",
				e.App, e.Target, e.OriginalPath, e.VaultPath)
		case !destExists:
			add("%s/%s: link target %s is missing", e.App, e.Target, e.VaultPath)
		default:
			add("%s/%s: %s", e.App, e.Target, detail)
		}
	}

	// Journals from interrupted moves.
	journals, _ := mover.ListJournals(vault)
	for _, j := range journals {
		add("%s/%s: incomplete move (state %s); use `midas rollback %s`", j.App, j.Target, j.State, j.App)
	}

	// Unverified or abandoned sets, stale locks.
	mgr := c.backupManager(vault)
	sets, _ := mgr.List("")
	for _, s := range sets {
		switch s.State {
		case backup.StateCreating:
			add("backup %s (%s/%s) is incomplete; it cannot be deleted until repaired", s.ID, s.App, s.Target)
		case backup.StateVerified:
			add("backup %s (%s/%s) is verified but its move never finished", s.ID, s.App, s.Target)
		}
		if _, err := os.Lstat(s.LockPath()); err == nil {
			lock, lerr := s.Lock()
			if lerr == nil && lock.PID != 0 && !platform.Current().IsProcessAlive(lock.PID) {
				add("backup %s has a stale LOCK from pid %d", s.ID, lock.PID)
				if c.shouldRepair(*repair) && !c.G.DryRun {
					if err := os.Remove(s.LockPath()); err == nil {
						c.UI.Successf("removed stale LOCK for backup %s", s.ID)
						problems = problems[:len(problems)-1]
					}
				}
			}
		}
	}

	// Orphaned partial copies.
	defs, _ := apps.All()
	for _, def := range defs {
		targets, _ := resolveTargets(def, vault)
		for _, t := range targets {
			partial := t.Dest + ".midas-partial"
			if mover.PathExists(partial) && !mover.JournalExists(vault, def.ID, t.Name) {
				add("orphaned partial copy %s", partial)
				if c.shouldRepair(*repair) && !c.G.DryRun {
					if err := mover.RemoveAllOwned(partial); err == nil {
						c.UI.Successf("removed %s", partial)
						problems = problems[:len(problems)-1]
					}
				}
			}
		}
	}

	if c.G.JSON {
		out := map[string]any{"issues": problems, "ok": len(problems) == 0}
		if len(problems) == 0 {
			out["issues"] = []string{}
		}
		_ = c.UI.PrintJSON(out)
	} else if len(problems) == 0 {
		c.UI.Successf("no problems found")
	} else {
		c.UI.Infof("%d problem(s):", len(problems))
		for _, p := range problems {
			c.UI.Infof("  - %s", p)
		}
		c.UI.Infof("safe fixes can be applied with `midas doctor --repair`")
	}
	if len(problems) > 0 {
		return silent(failf(ExitUnhealthy, "%d problem(s) remaining", len(problems)))
	}
	return nil
}

func (c *Ctx) shouldRepair(repairFlag bool) bool {
	if repairFlag {
		return true
	}
	if c.G.Yes {
		return true
	}
	ok, err := c.UI.Confirm("Repair this now?", false)
	return err == nil && ok
}

// rebuildManifest scans every known target for links pointing into the vault.
func rebuildManifest(vault string) (*manifest.Manifest, error) {
	defs, err := apps.All()
	if err != nil {
		return nil, err
	}
	m := manifest.Empty()
	for _, def := range defs {
		for _, t := range def.Targets {
			if t.Path() == "" {
				continue
			}
			src, err := apps.ExpandTarget(t)
			if err != nil {
				continue
			}
			link, isLink, err := platform.Current().ReadLink(src)
			if err != nil || !isLink {
				continue
			}
			if !within(link, vault) {
				continue
			}
			m.Upsert(manifest.Entry{
				App: def.ID, Target: t.Name,
				OriginalPath: src, VaultPath: link,
				LinkType: platform.LinkType(),
			})
		}
	}
	return m, nil
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !hasDotDotPrefix(rel))
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.'
}
