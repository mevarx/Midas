package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/platform"
)

func (c *Ctx) cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	repair := fs.Bool("repair", false, "apply safe, explicitly planned repairs")
	reportOnly := fs.Bool("report-only", false, "inspect state without applying repairs (default)")
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if *repair && *reportOnly {
		return failf(ExitUsage, "--repair and --report-only cannot be used together")
	}
	vault, err := c.Vault()
	if err != nil {
		return err
	}
	if *repair && !c.G.DryRun {
		release, lerr := c.acquireVaultLock(vault)
		if lerr != nil {
			return lerr
		}
		defer release()
	}

	report := DoctorReport{Findings: []DoctorFinding{}, Operations: []RecoveryOperation{}, Issues: []string{}}
	add := func(code, severity, app, target, path, message, next string) {
		report.Findings = append(report.Findings, DoctorFinding{
			Code: code, Severity: severity, App: app, Target: target,
			Path: path, Message: message, RecommendedAction: next,
		})
		if severity != "info" {
			report.Issues = append(report.Issues, message)
		}
	}
	if fi, err := os.Stat(vault); err != nil || !fi.IsDir() {
		return failf(ExitUnhealthy, "vault %q is not reachable (is the drive mounted?)", vault)
	}

	manifestPath := filepath.Join(vault, ".midas", "manifest.json")
	lockPath := filepath.Join(vault, ".midas", "LOCK")
	if holder, holderErr := platform.ReadLockHolder(lockPath); holderErr == nil {
		hostname, _ := os.Hostname()
		sameHost := holder.Hostname == "" || platform.EnvKeysEqual(holder.Hostname, hostname)
		if holder.PID != 0 && holder.PID != int32(os.Getpid()) && sameHost && platform.Current().IsProcessAlive(holder.PID) {
			where := ""
			if holder.Hostname != "" {
				where = " on " + holder.Hostname
			}
			started := ""
			if !holder.StartedAt.IsZero() {
				started = " since " + holder.StartedAt.UTC().Format(time.RFC3339)
			}
			add("lock.vault_held", "warning", "", "", lockPath, fmt.Sprintf("vault lock is held by pid %d%s%s", holder.PID, where, started), "wait for the other operation to finish")
		} else {
			add("lock.vault_stale", "info", "", "", lockPath, fmt.Sprintf("vault lock file records pid %d; no other live process holds the advisory lock. The OS releases it when its process exits, so a leftover file is harmless and is reused", holder.PID), "no action required")
		}
	} else if !errors.Is(holderErr, os.ErrNotExist) {
		add("lock.vault_unreadable", "warning", "", "", lockPath, "vault lock metadata is unreadable: "+holderErr.Error(), "inspect the lock file and remove it only if no Midas operation is running")
	}
	_, statErr := os.Lstat(manifestPath)
	manifestMissing := errors.Is(statErr, os.ErrNotExist)
	m, manifestErr := manifest.Load(vault)
	if statErr != nil && !manifestMissing {
		add("manifest.unreadable", "critical", "", "", manifestPath, "manifest cannot be inspected: "+statErr.Error(), "restore vault access and rerun `midas doctor`")
	}
	if manifestErr != nil || manifestMissing {
		rebuilt, rebuildErr := rebuildManifest(vault)
		canRebuild := rebuildErr == nil && rebuilt != nil
		if canRebuild && len(rebuilt.Entries) > 0 {
			if *repair && !c.G.DryRun {
				c.UI.Infof("repair plan: rebuild %s from validated links that resolve inside the vault", manifestPath)
				if err := rebuilt.Save(vault); err != nil {
					add("manifest.rebuild_failed", "critical", "", "", manifestPath, "manifest rebuild failed: "+err.Error(), "preserve the manifest and inspect the vault before retrying")
					m = manifest.Empty()
				} else {
					m = rebuilt
					add("manifest.rebuilt", "info", "", "", manifestPath, "manifest rebuilt from validated links", "no further action required")
				}
			} else {
				m = rebuilt
				code, message := "manifest.missing", "manifest is missing; known links were found in the vault"
				if manifestErr != nil {
					code, message = "manifest.invalid", "manifest is invalid: "+manifestErr.Error()
				}
				add(code, "error", "", "", manifestPath, message, "review the proposed reconstruction and run `midas doctor --repair` to save it")
			}
		} else if manifestErr != nil {
			m = manifest.Empty()
			add("manifest.invalid", "critical", "", "", manifestPath, "manifest is invalid and no safe reconstruction was possible", "preserve the file and use the recovery guidance in docs/SAFETY.md")
		} else {
			m = manifest.Empty()
			add("manifest.missing", "info", "", "", manifestPath, "no migration manifest exists yet", "no action is needed until a migration is recorded")
		}
	}
	if m == nil {
		m = manifest.Empty()
	}

	// Moved targets are always inspected first; repairs re-check these
	// preconditions immediately before creating a link.
	for i := range m.Entries {
		e := &m.Entries[i]
		ok, detail := mover.HealthCheck(e.OriginalPath, e.VaultPath)
		if ok {
			add("migration.healthy", "info", e.App, e.Target, e.OriginalPath, "migration link and vault destination are healthy", "no action required")
			continue
		}
		srcExists := mover.PathExists(e.OriginalPath)
		destExists := mover.DirExists(e.VaultPath)
		code, message, next := "link.unexpected", detail, "inspect both paths and preserve all copies before making changes"
		repairable := destExists && !srcExists
		switch {
		case repairable:
			code, message, next = "link.missing", fmt.Sprintf("link is missing at %s; vault copy is intact", e.OriginalPath), fmt.Sprintf("create link %s -> %s", e.OriginalPath, e.VaultPath)
		case destExists && srcExists:
			code, message, next = "original.replaced", fmt.Sprintf("%s is a real folder again; vault copy remains at %s", e.OriginalPath, e.VaultPath), "compare both trees and merge manually; doctor will not overwrite either copy"
		case !destExists:
			code, message, next = "destination.unavailable", fmt.Sprintf("vault destination %s is unavailable", e.VaultPath), "check whether the vault drive is mounted and do not recreate or remove the original"
		}
		if repairable && *repair && !c.G.DryRun {
			c.UI.Infof("repair plan: create link %s -> %s (no source directory will be overwritten)", e.OriginalPath, e.VaultPath)
			if !mover.PathExists(e.OriginalPath) && mover.DirExists(e.VaultPath) {
				if err := platform.Current().CreateLink(e.OriginalPath, e.VaultPath); err == nil {
					if healthy, _ := mover.HealthCheck(e.OriginalPath, e.VaultPath); healthy {
						add("link.repaired", "info", e.App, e.Target, e.OriginalPath, "missing link recreated and health-checked", "no further action required")
						continue
					}
				}
			}
			message = "link repair preconditions changed or the repaired link failed its health check"
			next = "preserve the source and vault copy and inspect the paths manually"
		}
		add(code, "error", e.App, e.Target, e.OriginalPath, message, next)
	}

	journals, corruptJournals, scanErr := mover.ScanJournals(vault)
	if scanErr != nil {
		add("journal.scan_failed", "critical", "", "", filepath.Join(vault, ".midas", "journal"), "journal directory cannot be inspected: "+scanErr.Error(), "restore vault access; do not start another operation")
	}
	for _, path := range corruptJournals {
		add("journal.corrupt", "critical", "", "", path, "journal is unreadable or fails schema, state, identity, or path validation; it was preserved", "preserve the journal and compare it with filesystem state before recovery")
	}

	mgr := c.backupManager(vault)
	sets, listErr := mgr.List("")
	if listErr != nil {
		add("backup.scan_failed", "critical", "", "", mgr.Dir, "backup sets cannot be inspected: "+listErr.Error(), "restore access to the backup directory and rerun doctor")
	}
	backupByID := make(map[string]*backup.Set, len(sets))
	for _, s := range sets {
		backupByID[s.ID] = s
		if s.State == "unreadable" {
			add("backup.invalid", "critical", s.App, s.Target, s.Dir, "backup metadata is missing or invalid; the set was preserved", "do not delete or use this set until its metadata has been inspected")
		}
		if s.State == backup.StateCreating {
			add("backup.incomplete", "error", s.App, s.Target, s.Dir, fmt.Sprintf("backup %s is incomplete", s.ID), "preserve the set and inspect its files before recovery")
		}
		if s.State == backup.StateVerified {
			add("backup.uncommitted", "warning", s.App, s.Target, s.Dir, fmt.Sprintf("verified backup %s has no completed move", s.ID), "retain it until the related operation is reconciled")
		}
		if _, err := os.Lstat(s.LockPath()); err == nil {
			lock, lerr := s.Lock()
			if lerr == nil && lock.PID != 0 && !platform.Current().IsProcessAlive(lock.PID) {
				add("lock.stale", "warning", s.App, s.Target, s.LockPath(), fmt.Sprintf("operation lock belongs to inactive pid %d", lock.PID), "run `midas doctor --repair` to remove the lock after revalidation")
				if *repair && !c.G.DryRun {
					c.UI.Infof("repair plan: remove stale lock %s", s.LockPath())
					current, currentErr := s.Lock()
					lockInfo, statErr := os.Lstat(s.LockPath())
					if currentErr == nil && statErr == nil && lockInfo.Mode().IsRegular() && current.PID == lock.PID && current.StartedAt.Equal(lock.StartedAt) && !platform.Current().IsProcessAlive(current.PID) {
						if err := os.Remove(s.LockPath()); err == nil {
							add("lock.repaired", "info", s.App, s.Target, s.LockPath(), "stale operation lock removed after revalidation", "no further action required")
						}
					}
				}
			} else if lerr != nil {
				add("lock.invalid", "error", s.App, s.Target, s.LockPath(), "operation lock metadata is unreadable", "preserve the lock and inspect whether its process is still active")
			}
		}
	}

	referencedBackups := map[string]bool{}
	for _, e := range m.Entries {
		if e.Backup.ID == "" {
			continue
		}
		referencedBackups[e.Backup.ID] = true
		s, ok := backupByID[e.Backup.ID]
		if !ok {
			add("backup.missing", "error", e.App, e.Target, e.Backup.Path, fmt.Sprintf("manifest references missing backup %s", e.Backup.ID), "search the configured backup root and preserve any matching files")
			continue
		}
		if s.State == "unreadable" {
			continue
		}
		if err := backup.Verify(s); err != nil {
			add("backup.invalid", "critical", e.App, e.Target, s.Dir, fmt.Sprintf("referenced backup %s failed verification: %v", s.ID, err), "keep the set and use manual recovery from readable files")
		}
	}
	for _, s := range sets {
		if !referencedBackups[s.ID] && s.State != "unreadable" {
			add("backup.unreferenced", "info", s.App, s.Target, s.Dir, fmt.Sprintf("backup %s is not referenced by the current manifest and was preserved", s.ID), "review its operation history; doctor will not infer that it is disposable")
		}
	}

	journalByTarget := make(map[string]*mover.Journal, len(journals))
	for _, j := range journals {
		key := j.App + "/" + j.Target
		journalByTarget[key] = j
		op := recoveryOperation(vault, j, m, backupByID, true)
		if j.State != mover.StateDone && j.State != mover.StateRolledBack {
			next := fmt.Sprintf("review the report, then use `midas rollback %s` if its source is valid", j.App)
			if op.ResumeAvailable {
				next = fmt.Sprintf("review the report, then use `midas resume %s` to restart the verified copy", j.App)
			}
			add("journal.incomplete", "error", j.App, j.Target, mover.JournalPath(vault, j.App, j.Target), fmt.Sprintf("operation %s is incomplete at state %s", j.OperationID, j.State), next)
		}
		if j.OperationID == "" {
			add("journal.legacy", "warning", j.App, j.Target, mover.JournalPath(vault, j.App, j.Target), "journal predates operation identity metadata", "preserve all paths and use the recovery report before taking action")
		}
		report.Operations = append(report.Operations, op)
		if !mover.PathExists(j.Source) && !mover.PathExists(j.Dest) && !mover.PathExists(j.Partial) && !mover.PathExists(j.Bak) && (j.BackupID == "" || backupByID[j.BackupID] == nil) {
			add("journal.artifacts_missing", "critical", j.App, j.Target, mover.JournalPath(vault, j.App, j.Target), "journal has no corresponding source, destination, partial, original backup, or backup-set artifact", "preserve the journal and investigate external moves or volume changes")
		}
	}

	defs, defsErr := apps.All()
	if defsErr != nil {
		add("apps.scan_failed", "error", "", "", "", "app definitions cannot be inspected: "+defsErr.Error(), "fix the app definitions before attempting repair")
	} else {
		knownPartials := map[string]bool{}
		for _, def := range defs {
			targets, targetErr := resolveTargets(def, vault)
			if targetErr != nil {
				add("target.invalid", "warning", def.ID, "", "", targetErr.Error(), "review the target definition; no paths were changed")
				continue
			}
			for _, t := range targets {
				key := def.ID + "/" + t.Name
				for _, partial := range []string{t.Dest + ".midas-partial", t.Source + ".midas-partial"} {
					knownPartials[filepath.Clean(partial)] = true
					if mover.PathExists(partial) && journalByTarget[key] == nil {
						add("partial.orphaned", "error", def.ID, t.Name, partial, "partial directory has no matching journal and was preserved", "inspect its contents and ownership metadata manually; doctor will not delete it")
					}
					if !mover.PathExists(partial) && mover.PathExists(partial+".midas-owner.json") && journalByTarget[key] == nil {
						marker := partial + ".midas-owner.json"
						if *repair && !c.G.DryRun {
							c.UI.Infof("repair plan: remove stale ownership metadata %s (no data path is touched)", marker)
							// Revalidate immediately before removing anything:
							// the partial path and its journal must still be absent.
							if mover.PathExists(partial) || mover.PathExists(mover.JournalPath(vault, def.ID, t.Name)) {
								add("operation.marker_stale", "warning", def.ID, t.Name, marker, "ownership metadata changed while doctor was running; no cleanup was attempted", "rerun `midas doctor` after the in-flight operation finishes")
							} else if rerr := os.Remove(marker); rerr != nil {
								add("operation.marker_cleanup_failed", "warning", def.ID, t.Name, marker, "stale ownership metadata could not be removed: "+rerr.Error(), "remove the marker by hand only after confirming the partial path and journal are absent")
							} else {
								add("operation.marker_cleaned", "info", def.ID, t.Name, marker, "stale ownership metadata removed after revalidation", "no action required")
							}
						} else {
							add("operation.artifact_orphaned", "warning", def.ID, t.Name, marker, "ownership metadata exists without its operation directory or journal", "preserve the metadata for diagnosis")
						}
					} else if !mover.PathExists(partial) && mover.PathExists(partial+".midas-owner.json") {
						add("operation.marker_stale", "warning", def.ID, t.Name, partial+".midas-owner.json", "ownership metadata remains after the partial path changed; no cleanup was attempted", "compare the marker, journal state, and committed destination before removing metadata")
					}
				}
				bak := t.Source + ".midas-bak"
				if mover.PathExists(bak) && journalByTarget[key] == nil {
					add("original.backup_orphaned", "warning", def.ID, t.Name, bak, "preserved original backup has no matching journal", "compare it with the source and manifest before any cleanup")
				}
				if !mover.PathExists(bak) && mover.PathExists(bak+".midas-owner.json") && journalByTarget[key] == nil {
					add("operation.marker_orphaned", "warning", def.ID, t.Name, bak+".midas-owner.json", "original-backup ownership metadata has no matching path or journal", "preserve the marker and investigate its operation history")
				}
			}
		}
		appsRoot := filepath.Join(vault, "apps")
		_ = filepath.WalkDir(appsRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				return nil
			}
			if strings.HasSuffix(entry.Name(), ".midas-partial") && !knownPartials[filepath.Clean(path)] {
				add("partial.unknown", "warning", "", "", path, "unrecognized partial path was preserved", "inspect its owner and contents; doctor will not infer that it is disposable")
			}
			if strings.HasSuffix(entry.Name(), ".midas-owner.json") {
				partial := strings.TrimSuffix(path, ".midas-owner.json")
				if !knownPartials[filepath.Clean(partial)] {
					add("operation.marker_unknown", "warning", "", "", path, "unrecognized ownership metadata was preserved", "inspect the associated path and operation history manually")
				}
			}
			return nil
		})
	}

	for _, j := range journals {
		if _, entry := m.Find(j.App, j.Target); entry == nil {
			add("operation.filesystem_mismatch", "error", j.App, j.Target, mover.JournalPath(vault, j.App, j.Target), "journal has no matching manifest entry", "reconcile the journal and filesystem manually; automatic resume is unavailable for this state")
		}
	}

	for _, f := range report.Findings {
		if f.Severity == "critical" || f.Severity == "error" {
			report.OK = false
			break
		}
	}
	if !report.OK {
		// zero value means false; distinguish a clean report explicitly below.
	}
	if !hasUnhealthyFinding(report.Findings) {
		report.OK = true
	}

	if c.G.JSON {
		_ = c.UI.PrintJSON(report)
	} else if len(report.Findings) == 0 {
		c.UI.Successf("no problems found")
	} else {
		c.UI.Infof("%d finding(s):", len(report.Findings))
		for _, f := range report.Findings {
			where := ""
			if f.App != "" {
				where = f.App + "/" + f.Target + ": "
			}
			c.UI.Infof("  [%s] %s %s%s", f.Severity, f.Code, where, f.Message)
			if f.RecommendedAction != "" {
				c.UI.Infof("    next: %s", f.RecommendedAction)
			}
		}
		if !*repair {
			c.UI.Infof("no changes were made; safe repairs require the explicit `midas doctor --repair` flag")
		}
	}
	if !report.OK {
		return silent(failf(ExitUnhealthy, "%d unhealthy finding(s)", unhealthyCount(report.Findings)))
	}
	return nil
}

func recoveryOperation(vault string, j *mover.Journal, m *manifest.Manifest, sets map[string]*backup.Set, verifyBackup bool) RecoveryOperation {
	op := RecoveryOperation{
		OperationID: j.OperationID, Type: j.Operation, App: j.App, Target: j.Target,
		Source: j.Source, Destination: j.Dest, JournalState: j.State,
		ManifestState: "untracked", LinkState: "unknown", BackupID: j.BackupID,
		FilesystemChecks: []string{"journal path and schema", "source/destination existence", "manifest reference"},
		CompletedActions: []string{}, PendingActions: []string{},
		SafeNextActions: []string{"inspect the source, destination, partial and backup before rollback"},
		Uncertainties:   []string{},
		ResumeBlockers:  []string{},
	}
	if _, entry := m.Find(j.App, j.Target); entry != nil {
		op.ManifestState = "tracked"
	}
	if healthy, detail := mover.HealthCheck(j.Source, j.Dest); healthy {
		op.LinkState = "healthy"
		op.ResolvedLinkTarget = j.Dest
		op.CompletedActions = append(op.CompletedActions, "link health check")
	} else {
		op.LinkState = detail
		op.Uncertainties = append(op.Uncertainties, "the original path is not a verified link to the recorded destination")
	}
	if s := sets[j.BackupID]; s != nil {
		op.BackupState = s.State
		if verifyBackup {
			if err := backup.Verify(s); err == nil {
				op.BackupVerification = "verified"
			} else {
				op.BackupVerification = "failed"
				op.Uncertainties = append(op.Uncertainties, "backup verification failed")
			}
		} else {
			op.BackupVerification = "not rechecked; recorded state is " + s.State
		}
	} else if j.BackupID != "" {
		op.BackupVerification = "missing"
		op.Uncertainties = append(op.Uncertainties, "recorded backup set could not be found")
	} else {
		op.BackupVerification = "not recorded"
	}
	if mover.PathExists(j.Partial) {
		op.PendingActions = append(op.PendingActions, "inspect owned partial directory")
	}
	switch j.State {
	case mover.StateCopying:
		op.PendingActions = append(op.PendingActions, "copy source to an operation-owned partial and verify it")
	case mover.StateVerifying:
		op.CompletedActions = append(op.CompletedActions, "copy phase")
		op.PendingActions = append(op.PendingActions, "finish destination verification before committing")
	case mover.StateRenaming, mover.StateSwapping, mover.StateLinking, mover.StateCleanup:
		op.CompletedActions = append(op.CompletedActions, "destination copy phase")
		op.PendingActions = append(op.PendingActions, "reconcile source, destination, and link state")
	case mover.StateRestoring:
		op.PendingActions = append(op.PendingActions, "reconcile restore source and vault copy")
	case mover.StateRollingBack, mover.StateNeedsRecovery:
		op.PendingActions = append(op.PendingActions, "complete or retry recovery after validating its source")
	}
	if mover.PathExists(j.Source) {
		op.FilesystemChecks = append(op.FilesystemChecks, "source exists")
	}
	if mover.PathExists(j.Dest) {
		op.FilesystemChecks = append(op.FilesystemChecks, "destination exists")
	}
	if j.Operation != "move" {
		op.ResumeBlockers = append(op.ResumeBlockers, "only interrupted move operations can be resumed")
	}
	if j.State != mover.StateCopying && j.State != mover.StateVerifying {
		op.ResumeBlockers = append(op.ResumeBlockers, "journal is not at a supported copy checkpoint")
	}
	if !mover.ValidOperationID(j.OperationID) {
		op.ResumeBlockers = append(op.ResumeBlockers, "journal has no valid operation ID")
	}
	if _, err := mover.TreeSnapshot(j.Source); err != nil {
		op.ResumeBlockers = append(op.ResumeBlockers, "source is missing, unreadable, or not a real directory")
	}
	if mover.PathExists(j.Dest) {
		op.ResumeBlockers = append(op.ResumeBlockers, "destination already exists")
	}
	if mover.PathExists(j.Bak) || mover.PathExists(j.Bak+".midas-owner.json") {
		op.ResumeBlockers = append(op.ResumeBlockers, "original backup path already exists")
	}
	if !platform.SamePath(j.Partial, j.Dest+".midas-partial") {
		op.ResumeBlockers = append(op.ResumeBlockers, "partial path does not match the recorded destination")
	}
	if mover.PathExists(j.Partial) || mover.PathExists(j.Partial+".midas-owner.json") {
		if err := mover.ValidateOwnedPath(filepath.Dir(j.Partial), j.Partial, j.OperationID, "move-partial"); err != nil {
			op.ResumeBlockers = append(op.ResumeBlockers, "partial-directory ownership cannot be validated")
		}
	}
	if j.BackupID == "" && !j.NoBackup {
		op.ResumeBlockers = append(op.ResumeBlockers, "journal records neither a backup nor an explicit no-backup confirmation")
	}
	if j.BackupID != "" && (op.BackupVerification != "verified" || op.BackupState != backup.StateVerified && op.BackupState != backup.StateInUse) {
		op.ResumeBlockers = append(op.ResumeBlockers, "recorded backup is missing, invalid, or in an unsupported state")
	}
	if _, entry := m.Find(j.App, j.Target); entry != nil {
		op.ResumeBlockers = append(op.ResumeBlockers, "manifest already records this target")
	}
	if defs, err := apps.All(); err != nil {
		op.ResumeBlockers = append(op.ResumeBlockers, "app definitions could not be validated")
	} else {
		matched := false
		for _, def := range defs {
			if def.ID != j.App {
				continue
			}
			targets, err := resolveTargets(def, vault)
			if err != nil {
				break
			}
			for _, target := range targets {
				if target.Name == j.Target && platform.SamePath(target.Source, j.Source) && platform.SamePath(target.Dest, j.Dest) {
					matched = true
				}
			}
		}
		if !matched {
			op.ResumeBlockers = append(op.ResumeBlockers, "journal paths do not match the current app definition")
		}
	}
	op.ResumeAvailable = len(op.ResumeBlockers) == 0
	if op.ResumeAvailable {
		op.SafeNextActions = []string{fmt.Sprintf("review this report and run `midas resume %s` to restart the owned copy", j.App)}
	}
	if op.JournalState == mover.StateDone || op.JournalState == mover.StateRolledBack {
		op.SafeNextActions = []string{"remove a stale journal only after confirming the recorded final state"}
	} else {
		op.SafeNextActions = append(op.SafeNextActions, fmt.Sprintf("run `midas rollback %s` after confirming its source", j.App))
	}
	return op
}

func hasUnhealthyFinding(findings []DoctorFinding) bool {
	return unhealthyCount(findings) > 0
}

func unhealthyCount(findings []DoctorFinding) int {
	n := 0
	for _, f := range findings {
		if f.Severity == "critical" || f.Severity == "error" {
			n++
		}
	}
	return n
}

// rebuildManifest scans known targets for links pointing into the vault.
func rebuildManifest(vault string) (*manifest.Manifest, error) {
	defs, err := apps.All()
	if err != nil {
		return nil, err
	}
	m := manifest.Empty()
	for _, def := range defs {
		for _, t := range def.Targets {
			if t.Path() == "" {
				continue
			}
			src, err := apps.ExpandTarget(t)
			if err != nil {
				continue
			}
			link, isLink, err := platform.Current().ReadLink(src)
			if err != nil || !isLink || !within(link, vault) {
				continue
			}
			m.Upsert(manifest.Entry{App: def.ID, Target: t.Name, OriginalPath: src, VaultPath: link, LinkType: platform.LinkType()})
		}
	}
	return m, nil
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || rel != ".." && !filepath.IsAbs(rel) && !hasDotDotPrefix(rel))
}

func hasDotDotPrefix(rel string) bool { return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' }
