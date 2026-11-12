package cli

import (
	"flag"

	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/platform"
	"github.com/mevarx/Midas/internal/ui"
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
	VaultFree       uint64              `json:"vault_free_bytes"`
	Entries         []statusEntry       `json:"entries"`
	Journals        int                 `json:"in_progress_journals"`
	Operations      []RecoveryOperation `json:"operations"`
	CorruptJournals []string            `json:"corrupt_journals"`
	Healthy         bool                `json:"healthy"`
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
	m, err := manifest.Load(vault)
	if err != nil {
		return failf(ExitUnhealthy, "%v", err)
	}
	rep := statusReport{Healthy: true, Entries: []statusEntry{}, Operations: []RecoveryOperation{}, CorruptJournals: []string{}}
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
	journals, corrupt, scanErr := mover.ScanJournals(vault)
	rep.CorruptJournals = corrupt
	if scanErr != nil || len(corrupt) > 0 {
		rep.Healthy = false
	}
	sets, _ := c.backupManager(vault).List("")
	backupSets := make(map[string]*backup.Set, len(sets))
	for _, set := range sets {
		backupSets[set.ID] = set
	}
	for _, j := range journals {
		if j.State != mover.StateDone && j.State != mover.StateRolledBack {
			rep.Journals++
			rep.Healthy = false
			rep.Operations = append(rep.Operations, recoveryOperation(vault, j, m, backupSets, false))
		}
	}

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
			c.UI.Warnf("%d interrupted operation(s) found; review `midas doctor --json` before `midas resume` or `midas rollback`", rep.Journals)
			for _, op := range rep.Operations {
				c.UI.Infof("  %s %s/%s: %s (operation %s)", op.Type, op.App, op.Target, op.JournalState, op.OperationID)
			}
		}
		if len(rep.CorruptJournals) > 0 {
			c.UI.Warnf("%d corrupt journal file(s) were preserved; run `midas doctor`", len(rep.CorruptJournals))
		}
	}
	if !rep.Healthy {
		return silent(failf(ExitUnhealthy, "one or more moved targets are unhealthy"))
	}
	return nil
}
