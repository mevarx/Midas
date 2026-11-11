package cli

import (
	"errors"
	"flag"

	"github.com/midas-cli/midas/internal/runner"
)

// cmdRun returns the child's exit code directly.
func (c *Ctx) cmdRun(args []string) int {
	left, right := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			left = args[:i]
			right = args[i+1:]
			break
		}
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	cleanTmp := fs.Bool("clean-tmp", false, "remove this app's temp files after the child exits")
	allowFallback := fs.Bool("allow-fallback", false, "allow the system temp directory when the vault is unreachable")
	if err := fs.Parse(left); err != nil {
		c.UI.Errorf("%v", err)
		return ExitUsage
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		c.UI.Errorf("%v", err)
		return ExitUsage
	}
	if fs.NArg() != 1 {
		c.UI.Errorf("usage: midas run <app> [flags] -- <command> [args...]")
		return ExitUsage
	}
	if len(right) == 0 {
		c.UI.Errorf("no command given; usage: midas run <app> -- <command> [args...]")
		return ExitUsage
	}
	appID := fs.Arg(0)
	def, err := c.app(appID)
	if err != nil {
		c.UI.Errorf("%v", err)
		return exitCode(err)
	}
	vault, err := c.requireVault()
	if err != nil {
		c.UI.Errorf("%v", err)
		return exitCode(err)
	}
	vaultApp := vaultAppDir(vault, def.ID)
	fallback := *allowFallback || c.Cfg.AllowFallback
	code, err := runner.Run(runner.Options{
		Vault:         vault,
		VaultApp:      vaultApp,
		Env:           def.RenderEnv(vaultApp),
		AllowFallback: fallback,
		CleanTmp:      *cleanTmp,
		Command:       right,
		Stdin:         c.UI.In,
		Stdout:        c.UI.Out,
		Stderr:        c.UI.Err,
	})
	if err != nil {
		c.UI.Errorf("%v", err)
		var ee *runner.EnvError
		if errors.As(err, &ee) {
			return ExitVaultUnreach
		}
		return ExitGeneral
	}
	return code
}
