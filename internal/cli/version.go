package cli

import (
	"flag"

	"github.com/midas-cli/midas/internal/version"
)

func (c *Ctx) cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	commit := fs.Bool("commit", false, "include the build commit")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if c.G.JSON {
		value := map[string]any{"version": version.Version}
		if *commit {
			value["commit"] = version.Commit
		}
		return c.UI.PrintJSON(value)
	}
	if *commit {
		c.UI.Infof("midas %s (%s)", version.Version, version.Commit)
	} else {
		c.UI.Infof("midas %s", version.Version)
	}
	return nil
}
