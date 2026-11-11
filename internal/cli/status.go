package cli

import (
	"flag"

	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/mover"
	"github.com/midas-cli/midas/internal/platform"
	"github.com/midas-cli/midas/internal/ui"
)

type statusEntry struct {
	App      string `json:"app"`
	Target   string `json:"target"`
	Original string `json:"original_path"`
	Vault    string `json:"vault_path"`
	Healthy  bool   `json:"healthy"`
	Detail   string `json:"detail,omitempty"`
	Backup   string `json:"backup_id,omitempty"`
}

type statusReport struct {
	VaultFree uint64        `json:"vault_free_bytes"`
	Entries   []statusEntry `json:"entries"`
	Journals  int           `json:"in_progress_journals"`
	Healthy   bool          `json:"healthy"`
}

func (c *Ctx) cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
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
	c.sweepRetention(vault)

	m, err := manifest.Load(vault)
	if err != nil {
		return failf(ExitUnhealthy, "%v", err)
	}
	rep := statusReport{Healthy: true}
	if free, ferr := platform.Current().FreeSpace(vault); ferr == nil {
		rep.VaultFree = free
	}
	for _, e := range m.Entries {
		ok, detail := mover.HealthCheck(e.OriginalPath, e.VaultPath)
		se := statusEntry{
			App: e.App, Target: e.Target, Original: e.OriginalPath,
			Vault: e.VaultPath, Healthy: ok, Detail: detail, Backup: e.Backup.ID,
		}
		if !ok {
			rep.Healthy = false
		}
		rep.Entries = append(rep.Entries, se)
	}
	journals, _ := mover.ListJournals(vault)
	rep.Journals = len(journals)

	if c.G.JSON {
		if err := c.UI.PrintJSON(rep); err != nil {
			return err
		}
	} else {
		if len(rep.Entries) == 0 {
			c.UI.Infof("no targets have been moved yet")
		} else {
			rows := make([][]string, len(rep.Entries))
			for i, e := range rep.Entries {
				health := "ok"
				detail := ""
				if !e.Healthy {
					health = "UNHEALTHY"
					detail = e.Detail
				}
				rows[i] = []string{e.App, e.Target, e.Original, health, detail}
			}
			c.UI.Table([]string{"APP", "TARGET", "ORIGINAL", "HEALTH", "DETAIL"}, rows)
			c.UI.Infof("vault free space: %s", ui.FormatBytes(rep.VaultFree))
		}
		if rep.Journals > 0 {
			c.UI.Warnf("%d in-progress move journal(s) found; run `midas doctor`", rep.Journals)
		}
	}
	if !rep.Healthy {
		return silent(failf(ExitUnhealthy, "one or more moved targets are unhealthy"))
	}
	return nil
}
