package cli

import (
	"flag"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/ui"
)

type listRow struct {
	App    string `json:"app"`
	Target string `json:"target"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Size   uint64 `json:"size_bytes"`
	State  string `json:"state"`
}

func (c *Ctx) cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	vault, err := c.requireVault()
	if err != nil {
		return err
	}
	m, err := manifest.Load(vault)
	if err != nil {
		return err
	}
	defs, err := apps.All()
	if err != nil {
		return err
	}

	var rows []listRow
	for _, def := range defs {
		targets, err := resolveTargets(def, vault)
		if err != nil {
			c.UI.Warnf("%s: %v", def.ID, err)
			continue
		}
		for _, t := range targets {
			_, entry := m.Find(def.ID, t.Name)
			st := stateLabel(t, entry != nil)
			if st == "moved" && entry == nil {
				st = "partial"
			}
			var size uint64
			if t.Moved {
				size, _ = dirStats(t.Dest)
			} else if t.Exists {
				size, _ = dirStats(t.Source)
			}
			rows = append(rows, listRow{
				App: def.ID, Target: t.Name, Path: t.Source,
				Exists: t.Exists, Size: size, State: st,
			})
		}
	}

	if c.G.JSON {
		return c.UI.PrintJSON(rows)
	}
	if len(rows) == 0 {
		c.UI.Infof("no apps define a target for this operating system")
		return nil
	}
	table := make([][]string, len(rows))
	for i, r := range rows {
		yn := "no"
		if r.Exists {
			yn = "yes"
		}
		table[i] = []string{r.App, r.Target, r.Path, yn, ui.FormatBytes(r.Size), r.State}
	}
	c.UI.Table([]string{"APP", "TARGET", "PATH", "EXISTS", "SIZE", "STATE"}, table)
	return nil
}
