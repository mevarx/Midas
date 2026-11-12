package cli

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/platform"
)

func (c *Ctx) cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	backupF := fs.String("backup-dir", "", "where backup sets are stored (default <vault>/backup)")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}

	vault := *vaultF
	if vault == "" {
		vault = c.Cfg.Vault
	}
	if vault == "" {
		if c.G.Yes {
			return failf(ExitUsage, "--vault is required with --yes")
		}
		var err error
		vault, err = c.UI.Prompt("Vault path (where moved data will live)", "")
		if err != nil {
			return err
		}
	}
	if vault == "" {
		return failf(ExitUsage, "a vault path is required")
	}
	vault, err := filepath.Abs(vault)
	if err != nil {
		return err
	}
	vault = filepath.Clean(vault)

	backupDir := *backupF
	if backupDir == "" && !c.G.Yes {
		def := filepath.Join(vault, "backup")
		answer, err := c.UI.Prompt("Backup directory (recommended: a different drive)", def)
		if err != nil {
			return err
		}
		backupDir = answer
	}
	if backupDir == "" {
		backupDir = filepath.Join(vault, "backup")
	}
	backupDir, err = filepath.Abs(backupDir)
	if err != nil {
		return err
	}

	if c.G.DryRun {
		c.UI.Infof("dry run: would set vault=%s backup_dir=%s", vault, backupDir)
		return nil
	}

	if err := os.MkdirAll(vault, 0o755); err != nil {
		return failf(ExitVaultUnreach, "create vault %q: %v", vault, err)
	}
	if err := c.ensureBackupLocation(backupDir); err != nil {
		return failf(ExitVaultUnreach, "%v", err)
	}

	home, _ := platform.HomeDir()
	if home != "" {
		if same, err := mover.SameVolume(home, vault); err == nil && same {
			c.UI.Warnf("the vault is on the same volume as your home directory; this frees no space. That is allowed, but most users want another drive.")
		}
	}
	if same, err := mover.SameVolume(vault, backupDir); err == nil && same {
		c.UI.Warnf("the backup directory is on the same volume as the vault: one drive failure would destroy both the moved data and its backup.")
		if !c.G.Yes {
			ok, err := c.UI.Confirm("Keep backups on the same volume anyway?", false)
			if err != nil {
				return err
			}
			if !ok {
				return failf(ExitUsage, "choose another backup location with --backup-dir <path>")
			}
		}
	}

	c.Cfg.Vault = vault
	c.Cfg.BackupDir = backupDir
	if c.Cfg.BackupDir == filepath.Join(vault, "backup") {
		c.Cfg.BackupDir = ""
	}
	if err := c.Cfg.Save(); err != nil {
		return err
	}
	c.G.Vault = vault
	c.Logf("init vault=%s backup_dir=%s", vault, c.Cfg.EffectiveBackupDir(vault))

	if c.G.JSON {
		return c.UI.PrintJSON(map[string]string{
			"vault":      vault,
			"backup_dir": c.Cfg.EffectiveBackupDir(vault),
			"config":     c.Cfg.Path,
		})
	}
	c.UI.Successf("vault: %s", vault)
	c.UI.Successf("backup directory: %s", c.Cfg.EffectiveBackupDir(vault))
	c.UI.Infof("config written to %s", c.Cfg.Path)
	c.UI.Infof("next: `midas list`, then `midas move <app>`")
	return nil
}
