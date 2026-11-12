package cli

import (
	"errors"
	"flag"
	"os"

	"github.com/mevarx/Midas/internal/apps"
)

func (c *Ctx) cmdApps(args []string) error {
	if len(args) == 0 {
		return failf(ExitUsage, "usage: midas apps <list|show|validate|path>")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("apps "+sub, flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	if err := fs.Parse(rest); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	switch sub {
	case "list":
		defs, err := apps.All()
		if err != nil {
			return err
		}
		if c.G.JSON {
			out := make([]map[string]string, len(defs))
			for i, d := range defs {
				out[i] = map[string]string{"id": d.ID, "name": d.Name, "source": d.Source}
			}
			return c.UI.PrintJSON(out)
		}
		table := make([][]string, len(defs))
		for i, d := range defs {
			src := "built-in"
			if d.Source != "" && d.Source[:min(8, len(d.Source))] != "builtin:" {
				src = d.Source
			}
			table[i] = []string{d.ID, d.Name, src}
		}
		c.UI.Table([]string{"ID", "NAME", "SOURCE"}, table)
		return nil

	case "show":
		if fs.NArg() != 1 {
			return failf(ExitUsage, "usage: midas apps show <app>")
		}
		def, err := c.app(fs.Arg(0))
		if err != nil {
			return err
		}
		if c.G.JSON {
			return c.UI.PrintJSON(def)
		}
		c.UI.Infof("%s (%s)", def.Name, def.ID)
		if def.Description != "" {
			c.UI.Infof("  %s", def.Description)
		}
		if def.Launch.Command != "" {
			c.UI.Infof("  launch command: %s", def.Launch.Command)
		}
		for _, t := range def.Targets {
			raw := t.Path()
			if raw == "" {
				continue
			}
			exp, err := apps.ExpandTarget(t)
			if err != nil {
				c.UI.Infof("  target %s: %s (cannot expand: %v)", t.Name, raw, err)
				continue
			}
			c.UI.Infof("  target %s: %s", t.Name, exp)
		}
		if len(def.Env) > 0 {
			c.UI.Infof("  env: %v", def.RenderEnv("<vault>/apps/"+def.ID))
		}
		if def.Backup != nil {
			c.UI.Infof("  essential backup include: %v", def.Backup.Include)
			c.UI.Infof("  essential backup exclude: %v", def.Backup.Exclude)
		}
		if def.Notes.Text != "" {
			c.UI.Infof("  note: %s", def.Notes.Text)
		}
		return nil

	case "validate":
		if fs.NArg() != 1 {
			return failf(ExitUsage, "usage: midas apps validate <file>")
		}
		path := fs.Arg(0)
		def, err := apps.DecodeFile(path)
		if err != nil {
			return failf(ExitUsage, "%v", err)
		}
		vault := c.Cfg.Vault
		errs := apps.ValidateForHost(def, vault)
		if c.G.JSON {
			out := map[string]any{"file": path, "id": def.ID, "valid": len(errs) == 0}
			if len(errs) > 0 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				out["errors"] = msgs
			}
			return c.UI.PrintJSON(out)
		}
		if len(errs) > 0 {
			for _, e := range errs {
				c.UI.Errorf("%s", e)
			}
			return failf(ExitUsage, "%s is not valid for this machine", path)
		}
		c.UI.Successf("%s is valid", path)
		for _, t := range def.Targets {
			if t.Path() == "" {
				continue
			}
			if exp, err := apps.ExpandTarget(t); err == nil {
				c.UI.Infof("  %s -> %s", t.Name, exp)
			}
		}
		return nil

	case "path":
		dir := apps.LocalDefinitionsDir()
		if c.G.JSON {
			return c.UI.PrintJSON(map[string]string{"path": dir})
		}
		c.UI.Infof("%s", dir)
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			c.UI.Infof("(does not exist yet; create it and drop a .toml file there)")
		}
		return nil

	default:
		return failf(ExitUsage, "unknown apps subcommand %q", sub)
	}
}
