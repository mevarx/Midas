package cli

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/midas-cli/midas/internal/platform"
)

func (c *Ctx) cmdShim(args []string) error {
	fs := flag.NewFlagSet("shim", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	dirF := fs.String("dir", "", "directory for the wrapper (must be on PATH)")
	commandF := fs.String("command", "", "real executable to wrap (default: the definition's launch command)")
	force := fs.Bool("force", false, "overwrite an existing wrapper")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas shim <app> [--dir <bin>]")
	}
	def, err := c.app(fs.Arg(0))
	if err != nil {
		return err
	}
	launch := def.Launch.Command
	real := *commandF
	if real == "" {
		if launch == "" {
			return failf(ExitUsage, "app %s does not define a launch command; pass --command", def.ID)
		}
		found, lerr := exec.LookPath(launch)
		if lerr != nil {
			return failf(ExitUsage, "cannot find %q on PATH; install the app or pass --command <path>", launch)
		}
		real = found
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return err
	}

	dir := *dirF
	if dir == "" {
		dir = c.Cfg.ShimDir
	}
	if dir == "" {
		dir = filepath.Join(platform.Current().ConfigDir(), "bin")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Dir(real), dir) {
		return failf(ExitUsage, "the real executable is already inside %s; choose another --dir to avoid calling the wrapper itself", dir)
	}
	midasExe, err := os.Executable()
	if err != nil {
		return err
	}
	midasExe, _ = filepath.Abs(midasExe)
	if strings.EqualFold(real, midasExe) {
		return failf(ExitUsage, "refusing to wrap midas itself")
	}

	base := launch
	if base == "" {
		base = filepath.Base(real)
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	target := filepath.Join(dir, platform.ShimFileName(base))
	if moverPathExists(target) && !*force {
		return failf(ExitUsage, "%s already exists; pass --force to overwrite", target)
	}
	script := platform.ShimScript(midasExe, def.ID, real)
	if c.G.DryRun {
		c.UI.Infof("dry run: would write %s:\n%s", target, script)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(script), 0o755); err != nil {
		return err
	}
	c.UI.Successf("wrote %s", target)
	if !pathContains(dir) {
		c.UI.Warnf("%s is not on your PATH; add it or use --dir to choose a directory that is", dir)
	}
	c.UI.Infof("typing %q now runs `midas run %s` with temp redirected to the vault", strings.TrimSuffix(base, ".cmd"), def.ID)
	return nil
}

func moverPathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func pathContains(dir string) bool {
	sep := string(os.PathListSeparator)
	for _, p := range strings.Split(os.Getenv("PATH"), sep) {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if strings.EqualFold(filepath.Clean(abs), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}
