// Package cli implements the midas command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/midas-cli/midas/internal/config"
	"github.com/midas-cli/midas/internal/ui"
)

// Exit codes (Section 6.12).
const (
	ExitOK              = 0
	ExitGeneral         = 1
	ExitUsage           = 2
	ExitRunning         = 3
	ExitNoSpace         = 4
	ExitVaultUnreach    = 5
	ExitVerifyFailed    = 6
	ExitUnhealthy       = 7
	ExitBackupFailed    = 8
	ExitBackupProtected = 9
)

// Global flags shared by every command.
type Global struct {
	Vault   string
	Config  string
	DryRun  bool
	Yes     bool
	JSON    bool
	Verbose bool
}

// Ctx carries everything a command needs.
type Ctx struct {
	G       Global
	Context context.Context
	Cfg     *config.Config
	UI      *ui.UI
	logF    *os.File
}

// codedError carries an exit code.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func exitCode(err error) int {
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ExitGeneral
}

func failf(code int, format string, a ...any) error {
	return &codedError{code: code, err: fmt.Errorf(format, a...)}
}

// Run executes the CLI and returns the process exit code.
func Run(args []string) int {
	return RunContext(args, context.Background())
}

// RunContext executes the CLI with cancellation tied to the calling process.
func RunContext(args []string, runContext context.Context) int {
	g, rest, err := parseGlobal(args)
	u := ui.New()
	u.JSON = g.JSON
	u.Verbose = g.Verbose
	if err != nil {
		if g.JSON {
			_ = u.PrintJSON(map[string]any{"error": map[string]any{"code": ExitUsage, "code_name": "usage_error", "message": err.Error()}})
		} else {
			u.Errorf("%v", err)
			fmt.Fprintln(u.Err, shortUsage())
		}
		return ExitUsage
	}
	cfg, err := config.Load(g.Config)
	if err != nil {
		if g.JSON {
			_ = u.PrintJSON(map[string]any{"error": map[string]any{"code": ExitGeneral, "code_name": "error", "message": err.Error()}})
		} else {
			u.Errorf("%v", err)
		}
		return ExitGeneral
	}
	ctx := &Ctx{G: g, Context: runContext, Cfg: cfg, UI: u}
	defer ctx.closeLog()

	if len(rest) == 0 {
		printHelp(u)
		return ExitOK
	}
	cmdName := rest[0]
	cmdArgs := rest[1:]

	switch cmdName {
	case "help", "--help", "-h":
		printHelp(u)
		return ExitOK
	case "version", "--version", "-V":
		err = ctx.cmdVersion(cmdArgs)
	case "init":
		err = ctx.cmdInit(cmdArgs)
	case "list":
		err = ctx.cmdList(cmdArgs)
	case "move":
		err = ctx.cmdMove(cmdArgs)
	case "resume":
		err = ctx.cmdResume(cmdArgs)
	case "plan":
		err = ctx.cmdPlan(cmdArgs)
	case "analyze":
		err = ctx.cmdAnalyze(cmdArgs)
	case "completion":
		err = ctx.cmdCompletion(cmdArgs)
	case "run":
		return ctx.cmdRun(cmdArgs) // run returns the child's exit code
	case "status":
		err = ctx.cmdStatus(cmdArgs)
	case "restore":
		err = ctx.cmdRestore(cmdArgs)
	case "rollback":
		err = ctx.cmdRollback(cmdArgs)
	case "backup":
		err = ctx.cmdBackup(cmdArgs)
	case "apps":
		err = ctx.cmdApps(cmdArgs)
	case "doctor":
		err = ctx.cmdDoctor(cmdArgs)
	case "shim":
		err = ctx.cmdShim(cmdArgs)
	default:
		if ctx.G.JSON {
			_ = u.PrintJSON(map[string]any{"error": map[string]any{"code": ExitUsage, "code_name": "usage_error", "message": fmt.Sprintf("unknown command %q", cmdName)}})
		} else {
			u.Errorf("unknown command %q", cmdName)
			fmt.Fprintln(u.Err, shortUsage())
		}
		return ExitUsage
	}
	if err != nil {
		if !isSilent(err) {
			if ctx.G.JSON {
				code := exitCode(err)
				_ = u.PrintJSON(map[string]any{"error": map[string]any{"code": code, "code_name": errorCodeName(code), "message": err.Error()}})
			} else {
				u.Errorf("%v", err)
			}
		}
		return exitCode(err)
	}
	return ExitOK
}

func errorCodeName(code int) string {
	switch code {
	case ExitOK:
		return "ok"
	case ExitUsage:
		return "usage_error"
	case ExitRunning:
		return "app_running"
	case ExitNoSpace:
		return "insufficient_space"
	case ExitVaultUnreach:
		return "vault_unreachable"
	case ExitVerifyFailed:
		return "verification_failed"
	case ExitUnhealthy:
		return "unhealthy"
	case ExitBackupFailed:
		return "backup_failed"
	case ExitBackupProtected:
		return "backup_protected"
	default:
		return "error"
	}
}

type silentError struct{ err error }

func (s *silentError) Error() string { return s.err.Error() }
func (s *silentError) Unwrap() error { return s.err }

func silent(err error) error { return &silentError{err: err} }
func isSilent(err error) bool {
	var s *silentError
	return errors.As(err, &s)
}

// parseGlobal consumes leading global flags, leaving the command and its
// arguments. Global flags are also registered on every command's FlagSet so
// they may appear after the subcommand.
func parseGlobal(args []string) (Global, []string, error) {
	var g Global
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			break
		}
		name, val, hasVal := strings.Cut(a, "=")
		needValue := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "--vault":
			g.Vault, err = needValue()
		case "--config":
			g.Config, err = needValue()
		case "--dry-run":
			g.DryRun = true
		case "--yes", "-y":
			g.Yes = true
		case "--json":
			g.JSON = true
		case "--verbose", "-v":
			g.Verbose = true
		case "--help", "-h":
			g.Verbose = false
			return g, []string{"help"}, nil
		default:
			return g, args[i:], nil
		}
		if err != nil {
			return g, nil, err
		}
		i++
	}
	return g, args[i:], nil
}

// addGlobal registers global flags on a command FlagSet.
func (c *Ctx) addGlobal(fs *flag.FlagSet) (vault, cfg *string, dry, yes, json, verbose *bool) {
	vault = fs.String("vault", c.G.Vault, "vault root directory")
	cfg = fs.String("config", c.G.Config, "config file path")
	dry = fs.Bool("dry-run", c.G.DryRun, "print the plan and change nothing")
	yes = fs.Bool("yes", c.G.Yes, "skip confirmation prompts")
	json = fs.Bool("json", c.G.JSON, "machine-readable output")
	verbose = fs.Bool("verbose", c.G.Verbose, "verbose output")
	return
}

func (c *Ctx) syncGlobal(vault, cfg *string, dry, yes, json, verbose *bool) error {
	c.G.Vault = *vault
	c.G.DryRun = *dry
	c.G.Yes = *yes
	c.G.JSON = *json
	c.G.Verbose = *verbose
	c.UI.JSON = *json
	c.UI.Verbose = *verbose
	if *cfg != c.G.Config {
		c.G.Config = *cfg
		loaded, err := config.Load(*cfg)
		if err != nil {
			return err
		}
		c.Cfg = loaded
	}
	return nil
}

// Vault resolves and validates the vault path.
func (c *Ctx) Vault() (string, error) {
	v := c.G.Vault
	if v == "" {
		v = c.Cfg.Vault
	}
	if v == "" {
		return "", failf(ExitUsage, "no vault configured; run `midas init --vault <path>`")
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// requireVault resolves the vault and checks it exists and is writable.
func (c *Ctx) requireVault() (string, error) {
	v, err := c.Vault()
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(v)
	if err != nil || !fi.IsDir() {
		return "", failf(ExitVaultUnreach, "vault %q is not reachable (is the drive mounted?)", v)
	}
	f, err := os.CreateTemp(v, ".midas-write-test-*")
	if err != nil {
		return "", failf(ExitVaultUnreach, "vault %q is not writable: %v", v, err)
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return v, nil
}

func (c *Ctx) confirm(question string, defaultYes bool) (bool, error) {
	if c.G.Yes {
		return true, nil
	}
	if !c.Cfg.Confirm {
		return true, nil
	}
	return c.UI.Confirm(question, defaultYes)
}

// Logf appends one line to the per-run log in <vault>/.midas/logs.
func (c *Ctx) Logf(format string, a ...any) {
	v := c.G.Vault
	if v == "" {
		v = c.Cfg.Vault
	}
	if v == "" {
		return
	}
	if c.logF == nil {
		dir := filepath.Join(v, ".midas", "logs")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return
		}
		name := filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
		f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		c.logF = f
	}
	fmt.Fprintf(c.logF, "%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

func (c *Ctx) closeLog() {
	if c.logF != nil {
		c.logF.Close()
	}
}

func shortUsage() string {
	return "usage: midas <command> [flags]\ncommands: init, list, move, resume, run, status, restore, rollback, plan, analyze, backup, apps, doctor, completion, shim"
}

func printHelp(u *ui.UI) {
	w := u.Human()
	fmt.Fprintf(w, `Midas — Everything you touch turns to storage space.

Usage: midas <command> [flags]

Commands:
  init                     set the vault and backup directory
  list                     show known apps, targets, sizes and state
  move <app>               move an app's folders into the vault and link them
  resume <app>             safely restart a validated interrupted copy
  plan <app>               show a read-only move plan
  analyze <app>            inspect targets and move readiness without changes
  run <app> -- <cmd...>    launch a command with temp redirected to the vault
  status                   check every moved target, link and disk space
  restore <app>            move an app's data back to its original location
  rollback <app>           undo a move from the best available source
  backup <list|show|verify|delete>   manage backup sets
  apps <list|show|validate|path>     inspect app definitions
  doctor                   read-only health report; --repair is explicit
  completion <shell>       print bash, zsh, fish or powershell completions
  shim <app>               create a wrapper that applies run automatically
  version                  print the version

Global flags:
  --vault <path>   override the configured vault
  --config <path>  config file path
  --dry-run        print the plan and change nothing
  --yes            skip confirmation prompts
  --json           machine-readable output
  --verbose        extra detail

Exit codes: 0 ok, 1 error, 2 usage, 3 app running, 4 no space,
            5 vault unreachable, 6 verification failed, 7 unhealthy,
            8 backup failed, 9 backup protected.
`)
}
