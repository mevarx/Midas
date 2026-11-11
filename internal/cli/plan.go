package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/midas-cli/midas/internal/apps"
	"github.com/midas-cli/midas/internal/backup"
	"github.com/midas-cli/midas/internal/manifest"
	"github.com/midas-cli/midas/internal/mover"
	"github.com/midas-cli/midas/internal/platform"
	"github.com/midas-cli/midas/internal/ui"
)

type planTarget struct {
	Name                 string   `json:"target"`
	Source               string   `json:"source"`
	Destination          string   `json:"destination"`
	DestinationMethod    string   `json:"destination_method"`
	State                string   `json:"state"`
	Bytes                uint64   `json:"source_bytes_estimate"`
	Files                int      `json:"source_files_estimate"`
	BackupBytes          int64    `json:"backup_bytes_estimate"`
	BackupFiles          int      `json:"backup_files_estimate"`
	SkippedFiles         int      `json:"backup_skipped_files_estimate"`
	VerifyMode           string   `json:"verification_mode"`
	Transitions          []string `json:"proposed_transitions"`
	ConfirmationRequired bool     `json:"confirmation_required"`
	Warnings             []string `json:"warnings"`
}

type planReport struct {
	SchemaVersion      int          `json:"schema_version"`
	ReadOnly           bool         `json:"read_only"`
	App                string       `json:"app"`
	Vault              string       `json:"vault"`
	BackupMode         string       `json:"backup_mode"`
	BackupLocation     string       `json:"backup_location"`
	Targets            []planTarget `json:"targets"`
	EstimatedVaultUse  uint64       `json:"estimated_vault_bytes_with_overhead"`
	EstimatedBackupUse uint64       `json:"estimated_backup_bytes_with_overhead"`
	VaultFreeBytes     *uint64      `json:"vault_free_bytes,omitempty"`
	BackupFreeBytes    *uint64      `json:"backup_free_bytes,omitempty"`
	Warnings           []string     `json:"warnings"`
	Mutations          []string     `json:"mutations"`
}

func (c *Ctx) cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	backupMode := fs.String("backup", "", "backup mode: full|essential|none (default from config)")
	var targetFlags stringList
	fs.Var(&targetFlags, "target", "plan only this target (repeatable)")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas plan <app> [--target <name>] [--backup full|essential|none]")
	}
	vault, err := c.Vault()
	if err != nil {
		return err
	}
	def, err := c.app(fs.Arg(0))
	if err != nil {
		return err
	}
	if errs := apps.ValidateForHost(def, vault); len(errs) > 0 {
		return failf(ExitUsage, "app definition %s is invalid:\n  %v", def.ID, errs)
	}
	mode := *backupMode
	if mode == "" {
		mode = c.Cfg.Backup
	}
	if mode != backup.ModeFull && mode != backup.ModeEssential && mode != backup.ModeNone {
		return failf(ExitUsage, "--backup must be full, essential or none")
	}
	for _, name := range targetFlags {
		if _, ok := def.TargetByName(name); !ok {
			return failf(ExitUsage, "app %s has no target %q", def.ID, name)
		}
	}
	wanted := make(map[string]bool, len(targetFlags))
	for _, name := range targetFlags {
		wanted[name] = true
	}
	report := planReport{
		SchemaVersion: 1, ReadOnly: true, App: def.ID, Vault: vault,
		BackupMode: mode, BackupLocation: c.Cfg.EffectiveBackupDir(vault),
		Targets: []planTarget{}, Warnings: []string{}, Mutations: []string{},
	}
	targets, err := resolveTargets(def, vault)
	if err != nil {
		return err
	}
	manifestState := manifest.Empty()
	if fi, statErr := os.Lstat(vault); statErr == nil && fi.IsDir() {
		manifestState, err = manifest.Load(vault)
		if err != nil {
			return failf(ExitUnhealthy, "manifest cannot be analyzed safely: %v", err)
		}
	}
	if processes, detectable := c.runningProcs(def); detectable && len(processes) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s appears to be running; close it before moving data", def.Name))
	} else if len(def.ProcessNames()) == 0 {
		report.Warnings = append(report.Warnings, "process detection is not configured; the move command will require explicit confirmation")
	}
	rule := def.EffectiveBackupRule()
	for _, t := range targets {
		if len(wanted) > 0 && !wanted[t.Name] {
			continue
		}
		item := planTarget{
			Name: t.Name, Source: t.Source, Destination: t.Dest,
			VerifyMode: c.Cfg.Verify, Transitions: []string{"preflight"},
			Warnings: []string{},
		}
		journal, journalErr := mover.LoadJournal(vault, def.ID, t.Name)
		if _, entry := manifestState.Find(def.ID, t.Name); entry != nil {
			item.State = "already moved"
			item.Warnings = append(item.Warnings, "target is already recorded in the manifest")
		} else if journalErr != nil {
			item.State = "invalid recovery journal"
			item.Warnings = append(item.Warnings, "journal is unreadable or invalid: "+journalErr.Error())
		} else if journal != nil {
			item.State = "interrupted operation"
			item.Warnings = append(item.Warnings, fmt.Sprintf("operation %s is at state %s; reconcile or resume it before starting another move", journal.OperationID, journal.State))
		} else if mover.PathExists(t.Dest+".midas-partial") || mover.PathExists(t.Source+".midas-partial") || mover.PathExists(t.Source+".midas-bak") {
			item.State = "recovery artifact conflict"
			item.Warnings = append(item.Warnings, "partial or preserved original backup exists and will not be overwritten")
		} else if pathOverlaps(t.Source, vault) {
			item.State = "vault overlaps source"
			item.Warnings = append(item.Warnings, "vault overlaps the source and could cause recursive copying")
		} else if t.IsLink {
			item.State = "link present"
			item.Warnings = append(item.Warnings, "source is already a link; no move is planned")
		} else if !t.Exists {
			item.State = "source missing"
			item.Warnings = append(item.Warnings, "source does not exist; it would be skipped")
		} else {
			item.State = "ready"
			if mover.PathExists(t.Dest) {
				item.State = "destination conflict"
				item.Warnings = append(item.Warnings, "destination already exists and will not be overwritten")
			} else if bytes, files, measureErr := mover.Measure(t.Source); measureErr != nil {
				item.State = "source unreadable"
				item.Warnings = append(item.Warnings, measureErr.Error())
			} else {
				item.Bytes, item.Files = bytes, files
				sameVolume, volumeErr := mover.SameVolume(t.Source, vault)
				if volumeErr != nil {
					item.Warnings = append(item.Warnings, "volume relationship unavailable; space estimate assumes a copy")
					sameVolume = false
				}
				if sameVolume {
					item.DestinationMethod = "same-volume rename"
				} else {
					item.DestinationMethod = "cross-volume verified copy"
					report.EstimatedVaultUse += bytes + bytes/10
				}
				if mode == backup.ModeFull {
					item.BackupBytes, item.BackupFiles = int64(bytes), files
				} else if mode == backup.ModeEssential {
					estimate, estimateErr := backup.EstimateBackup(t.Source, mode, rule)
					if estimateErr != nil {
						item.Warnings = append(item.Warnings, "backup estimate incomplete: "+estimateErr.Error())
					} else {
						item.BackupBytes, item.BackupFiles, item.SkippedFiles = estimate.Bytes, estimate.Files, estimate.SkippedFiles
					}
				}
				report.EstimatedBackupUse += uint64(item.BackupBytes + item.BackupBytes/10)
				item.ConfirmationRequired = mode == backup.ModeNone
				if sameVolume {
					item.Transitions = []string{"preflight", "backup", "rename", "link health check", "manifest commit", "complete"}
				} else {
					item.Transitions = []string{"preflight", "backup", "copy", "verify", "link health check", "manifest commit", "complete"}
				}
			}
		}
		for _, warning := range item.Warnings {
			report.Warnings = append(report.Warnings, def.ID+"/"+item.Name+": "+warning)
		}
		report.Targets = append(report.Targets, item)
	}
	if len(report.Targets) == 0 {
		report.Warnings = append(report.Warnings, "no targets are defined for this platform or selected filter")
	}
	if free, spaceErr := platform.Current().FreeSpace(vault); spaceErr == nil {
		report.VaultFreeBytes = &free
		if report.EstimatedVaultUse > free {
			report.Warnings = append(report.Warnings, "estimated vault space exceeds currently available space")
		}
	} else {
		report.Warnings = append(report.Warnings, "vault free space unavailable; destination space is an estimate")
	}
	if mode != backup.ModeNone {
		if free, spaceErr := platform.Current().FreeSpace(report.BackupLocation); spaceErr == nil {
			report.BackupFreeBytes = &free
			if sameVolume, err := mover.SameVolume(vault, report.BackupLocation); err == nil && sameVolume {
				report.Warnings = append(report.Warnings, "vault and backup location share a volume; one drive failure could affect both")
				if report.EstimatedVaultUse+report.EstimatedBackupUse > free {
					report.Warnings = append(report.Warnings, "combined estimated vault and backup use exceeds currently available space")
				}
			} else if report.EstimatedBackupUse > free {
				report.Warnings = append(report.Warnings, "estimated backup space exceeds currently available space")
			}
		} else {
			report.Warnings = append(report.Warnings, "backup free space unavailable; backup destination space is an estimate")
		}
	} else {
		report.Warnings = append(report.Warnings, "no backup selected; `move` will require explicit typed confirmation")
	}
	if c.G.JSON {
		return c.UI.PrintJSON(report)
	}
	c.UI.Infof("read-only plan for %s (backup=%s, verification=%s)", report.App, report.BackupMode, c.Cfg.Verify)
	for _, item := range report.Targets {
		c.UI.Infof("%s: %s", item.Name, item.State)
		c.UI.Infof("  %s -> %s", item.Source, item.Destination)
		if item.State == "ready" {
			c.UI.Infof("  source estimate: %s, %d file(s); backup estimate: %s, %d file(s)", ui.FormatBytes(item.Bytes), item.Files, ui.FormatBytes(uint64(item.BackupBytes)), item.BackupFiles)
			c.UI.Infof("  destination method: %s", item.DestinationMethod)
			c.UI.Infof("  transitions: %s", strings.Join(item.Transitions, " -> "))
		}
		for _, warning := range item.Warnings {
			c.UI.Warnf("%s", warning)
		}
	}
	c.UI.Infof("estimated destination use including 10%% overhead: %s", ui.FormatBytes(report.EstimatedVaultUse))
	c.UI.Infof("estimated backup use including 10%% overhead: %s", ui.FormatBytes(report.EstimatedBackupUse))
	c.UI.Infof("plan is read-only; estimates are best-effort and no paths were changed")
	return nil
}

func (c *Ctx) cmdAnalyze(args []string) error { return c.cmdPlan(args) }
