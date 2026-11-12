package cli

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/ui"
)

func (c *Ctx) cmdBackup(args []string) error {
	if len(args) == 0 {
		return failf(ExitUsage, "usage: midas backup <list|show|verify|delete>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return c.backupList(rest)
	case "show":
		return c.backupShow(rest)
	case "verify":
		return c.backupVerify(rest)
	case "delete", "rm":
		return c.backupDelete(rest)
	default:
		return failf(ExitUsage, "unknown backup subcommand %q", sub)
	}
}

func (c *Ctx) backupList(args []string) error {
	fs := flag.NewFlagSet("backup list", flag.ContinueOnError)
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
	appFilter := fs.Arg(0)
	mgr := c.backupManager(vault)
	sets, err := mgr.List(appFilter)
	if err != nil {
		return err
	}
	type row struct {
		ID        string    `json:"id"`
		App       string    `json:"app"`
		Target    string    `json:"target"`
		Mode      string    `json:"mode"`
		State     string    `json:"state"`
		Size      int64     `json:"size_bytes"`
		CreatedAt time.Time `json:"created_at"`
		Deletable bool      `json:"deletable"`
		Reason    string    `json:"reason,omitempty"`
	}
	var rows []row
	for _, s := range sets {
		r := row{ID: s.ID, App: s.App, Target: s.Target, Mode: s.Mode, State: s.State, Size: s.TotalBytes(), CreatedAt: s.CreatedAt}
		reason := backup.CheckDelete(s, c.guardForSet(vault, s))
		r.Deletable = reason == nil
		if reason != nil {
			r.Reason = reason.Error()
		}
		rows = append(rows, r)
	}
	if c.G.JSON {
		return c.UI.PrintJSON(rows)
	}
	if len(rows) == 0 {
		c.UI.Infof("no backup sets")
		return nil
	}
	table := make([][]string, len(rows))
	for i, r := range rows {
		del := "yes"
		if !r.Deletable {
			del = "no"
		}
		table[i] = []string{r.ID, r.App, r.Target, r.Mode, r.State, ui.FormatBytes(uint64(r.Size)), r.CreatedAt.Format("2006-01-02 15:04"), del}
	}
	c.UI.Table([]string{"ID", "APP", "TARGET", "MODE", "STATE", "SIZE", "CREATED", "DELETABLE"}, table)
	return nil
}

func (c *Ctx) backupShow(args []string) error {
	fs := flag.NewFlagSet("backup show", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas backup show <id>")
	}
	vault, err := c.requireVault()
	if err != nil {
		return err
	}
	s, err := c.backupManager(vault).Find(fs.Arg(0))
	if err != nil {
		return failf(ExitUsage, "%v", err)
	}
	reason := backup.CheckDelete(s, c.guardForSet(vault, s))
	if c.G.JSON {
		return c.UI.PrintJSON(map[string]any{
			"set": s, "deletable": reason == nil, "reason": errString(reason),
		})
	}
	c.UI.Infof("backup %s", s.ID)
	c.UI.Infof("  app:      %s (target %s)", s.App, s.Target)
	c.UI.Infof("  mode:     %s", s.Mode)
	c.UI.Infof("  state:    %s", backup.StateLabel(s.State))
	c.UI.Infof("  created:  %s", s.CreatedAt.Format(time.RFC3339))
	c.UI.Infof("  location: %s", s.Dir)
	c.UI.Infof("  original: %s", s.OriginalPath)
	c.UI.Infof("  files:    %d (%s)", len(s.Files), ui.FormatBytes(uint64(s.TotalBytes())))
	if s.RestorePoint.Attempted {
		if s.RestorePoint.Created {
			c.UI.Infof("  restore point: %s (sequence %d)", s.RestorePoint.Name, s.RestorePoint.Sequence)
		} else {
			c.UI.Infof("  restore point: not created: %s", s.RestorePoint.Reason)
		}
	}
	if len(s.Skipped) > 0 {
		c.UI.Infof("  not covered (%d):", len(s.Skipped))
		limit := len(s.Skipped)
		if limit > 50 {
			limit = 50
		}
		for _, sk := range s.Skipped[:limit] {
			c.UI.Infof("    %s (%s)", sk.Path, sk.Reason)
		}
		if len(s.Skipped) > limit {
			c.UI.Infof("    ... %d more", len(s.Skipped)-limit)
		}
	}
	if reason == nil {
		c.UI.Infof("  deletable: yes")
	} else {
		c.UI.Infof("  deletable: no — %s", reason)
	}
	return nil
}

func (c *Ctx) backupVerify(args []string) error {
	fs := flag.NewFlagSet("backup verify", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	if err := fs.Parse(args); err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if err := c.syncGlobal(vaultF, cfgF, dry, yes, jsonF, verbose); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas backup verify <id>")
	}
	vault, err := c.requireVault()
	if err != nil {
		return err
	}
	s, err := c.backupManager(vault).Find(fs.Arg(0))
	if err != nil {
		return failf(ExitUsage, "%v", err)
	}
	verr := backup.Verify(s)
	if c.G.JSON {
		out := map[string]any{"id": s.ID, "ok": verr == nil}
		if verr != nil {
			out["error"] = verr.Error()
		}
		return c.UI.PrintJSON(out)
	}
	if verr != nil {
		return failf(ExitVerifyFailed, "%v", verr)
	}
	c.UI.Successf("backup %s verified: %d files match their hashes", s.ID, len(s.Files))
	return nil
}

func (c *Ctx) backupDelete(args []string) error {
	fs := flag.NewFlagSet("backup delete", flag.ContinueOnError)
	fs.SetOutput(c.UI.Err)
	vaultF, cfgF, dry, yes, jsonF, verbose := c.addGlobal(fs)
	allCompleted := fs.Bool("all-completed", false, "delete every set that is currently deletable")
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
	mgr := c.backupManager(vault)

	if *allCompleted {
		sets, err := mgr.List("")
		if err != nil {
			return err
		}
		var due []*backup.Set
		for _, s := range sets {
			if backup.CheckDelete(s, c.guardForSet(vault, s)) == nil {
				due = append(due, s)
			}
		}
		if len(due) == 0 {
			c.UI.Infof("no backup sets are currently deletable")
			return nil
		}
		if c.G.DryRun {
			for _, s := range due {
				c.UI.Infof("dry run: would delete %s (%s/%s)", s.ID, s.App, s.Target)
			}
			return nil
		}
		if !c.G.Yes {
			ok, err := c.UI.ConfirmTyped(fmt.Sprintf("Delete %d backup set(s)?", len(due)), "delete all")
			if err != nil || !ok {
				return failf(ExitBackupProtected, "not confirmed; nothing was deleted")
			}
		}
		var failed int
		for _, s := range due {
			if derr := c.deleteBackupWithGuard(vault, s, true, nil); derr != nil {
				c.UI.Errorf("backup %s: %v", s.ID, derr)
				failed++
				continue
			}
			c.UI.Successf("deleted backup %s", s.ID)
		}
		if failed > 0 {
			return failf(ExitBackupProtected, "%d backup set(s) could not be deleted", failed)
		}
		return nil
	}

	if fs.NArg() != 1 {
		return failf(ExitUsage, "usage: midas backup delete <id> [--all-completed]")
	}
	s, err := mgr.Find(fs.Arg(0))
	if err != nil {
		return failf(ExitUsage, "%v", err)
	}
	if c.G.DryRun {
		if reason := backup.CheckDelete(s, c.guardForSet(vault, s)); reason != nil {
			return failf(ExitBackupProtected, "%v", reason)
		}
		c.UI.Infof("dry run: would delete backup %s at %s", s.ID, s.Dir)
		return nil
	}
	confirm := func(app, id string) (bool, error) {
		return c.UI.ConfirmTyped(fmt.Sprintf("Delete backup %s of %s?", id, app), app)
	}
	if err := c.deleteBackupWithGuard(vault, s, c.G.Yes, confirm); err != nil {
		return err
	}
	c.UI.Successf("deleted backup %s", s.ID)
	return nil
}

func (c *Ctx) deleteBackupWithGuard(vault string, s *backup.Set, skipConfirm bool, confirm func(string, string) (bool, error)) error {
	mgr := c.backupManager(vault)
	err := mgr.Delete(s, backup.DeleteOptions{
		Guard:       c.guardForSet(vault, s),
		SkipConfirm: skipConfirm,
		Confirm:     confirm,
	})
	if err == nil {
		c.Logf("deleted backup %s (%s/%s)", s.ID, s.App, s.Target)
	}
	var pe *backup.ProtectedError
	if errors.As(err, &pe) {
		return failf(ExitBackupProtected, "%v", err)
	}
	return err
}

// sweepRetention implements the "<N>d" policy on later runs.
func (c *Ctx) sweepRetention(vault string) {
	if c.G.DryRun {
		return
	}
	ret := c.Cfg.BackupRetention
	if !strings.HasSuffix(ret, "d") || ret == "keep" {
		return
	}
	days, err := strconv.Atoi(strings.TrimSuffix(ret, "d"))
	if err != nil || days <= 0 {
		return
	}
	sets, err := c.backupManager(vault).List("")
	if err != nil {
		return
	}
	for _, s := range sets {
		if s.State != backup.StateComplete {
			continue
		}
		if s.CreatedAt.IsZero() || time.Since(s.CreatedAt) < time.Duration(days)*24*time.Hour {
			continue
		}
		if derr := c.deleteBackupWithGuard(vault, s, true, nil); derr != nil {
			c.UI.Warnf("retention: backup %s was not deleted: %v", s.ID, derr)
			continue
		}
		c.UI.Infof("retention: deleted backup %s (older than %d days)", s.ID, days)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
