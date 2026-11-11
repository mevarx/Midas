package cli

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/mover"
	"github.com/midas-cli/midas/internal/platform"
	"github.com/midas-cli/midas/internal/ui"
)

func (c *Ctx) cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	keepVault := fs.Bool("keep-vault-copy", false, "keep the vault copy after restoring")
	force := fs.Bool("force", false, "restore even if the app appears to be running")
	var targetFlags stringList
	fs.Var(&targetFlags, "target", "restore only this target (repeatable)")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas restore <app>")
	}
	vault, err := c.requireVault()
	if err != nil {
		return err
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
	entries := m.ForApp(def.ID)
	wanted := map[string]bool{}
	for _, t := range targetFlags {
		wanted[t] = true
	}
	var selected []manifest.Entry
	for _, e := range entries {
		if len(wanted) > 0 && !wanted[e.Target] {
			continue
		}
		selected = append(selected, e)
	}
	if len(selected) == 0 {
		c.UI.Infof("nothing to restore for %s", def.ID)
		return nil
	}

	// Space on the original volume.
	var need uint64
	for _, e := range selected {
		need += e.Bytes + e.Bytes/10
	}
	free, ferr := platform.Current().FreeSpace(filepath.Dir(selected[0].OriginalPath))
	if ferr != nil {
		return failf(ExitGeneral, "cannot read free space on the original volume: %v", ferr)
	}
	if need > free {
		return failf(ExitNoSpace, "not enough space on the original volume: need about %s, %s free",
			ui.FormatBytes(need), ui.FormatBytes(free))
	}

	c.UI.Infof("Restore plan for %s:", def.ID)
	for _, e := range selected {
		c.UI.Infof("  %s: %s -> %s", e.Target, e.VaultPath, e.OriginalPath)
	}
	if c.G.DryRun {
		c.UI.Infof("dry run: nothing was changed")
		return nil
	}
	ok, err := c.confirm(fmt.Sprintf("Restore %d target(s) for %s?", len(selected), def.ID), true)
	if err != nil {
		return err
	}
	if !ok {
		return failf(ExitGeneral, "cancelled; nothing was changed")
	}

	for _, e := range selected {
		perr := mover.Restore(mover.RestoreOptions{
			App: e.App, Target: e.Target,
			Context: c.Context,
			Source:  e.OriginalPath, Dest: e.VaultPath, Vault: vault,
			KeepVaultCopy: *keepVault,
			Verify:        c.Cfg.Verify,
			Force:         *force,
			Manifest:      m,
			Stage:         func(stage, detail string) { c.UI.Verbosef("%s: %s", e.Target, detail) },
			Warn:          func(f string, a ...any) { c.UI.Warnf(f, a...) },
		})
		if perr != nil {
			return failf(ExitGeneral, "%v", perr)
		}
		c.UI.Successf("%s/%s restored to %s", e.App, e.Target, e.OriginalPath)
	}
	if c.G.JSON {
		return c.UI.PrintJSON(map[string]any{"app": def.ID, "restored": len(selected)})
	}
	return nil
}
