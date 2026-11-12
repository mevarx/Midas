package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/backup"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/mover"
	"github.com/mevarx/Midas/internal/platform"
)

// targetInfo is one target resolved for this host.
type targetInfo struct {
	Name       string
	Source     string // expanded original path
	Dest       string // vault path
	Exists     bool
	IsLink     bool
	LinkTarget string
	Moved      bool // healthy manifest-tracked move
	Broken     bool // link present but target unreachable
	Partial    bool // real folder where the manifest expects a link
}

func vaultAppDir(vault, appID string) string {
	return filepath.Join(vault, "apps", appID)
}

func destFor(vault, appID, targetName string) string {
	return filepath.Join(vault, "apps", appID, targetName)
}

// resolveTargets expands every target of an app for this OS.
func resolveTargets(def *apps.Definition, vault string) ([]targetInfo, error) {
	var out []targetInfo
	for _, t := range def.Targets {
		if t.Path() == "" {
			continue // not defined for this OS
		}
		src, err := apps.ExpandTarget(t)
		if err != nil {
			return nil, err
		}
		ti := targetInfo{Name: t.Name, Source: src, Dest: destFor(vault, def.ID, t.Name)}
		ti.Exists = mover.PathExists(src)
		if target, isLink, err := platform.Current().ReadLink(src); err == nil && isLink {
			ti.IsLink = true
			ti.LinkTarget = target
			if mover.DirExists(target) {
				ti.Moved = true
			} else {
				ti.Broken = true
			}
		}
		out = append(out, ti)
	}
	return out, nil
}

func (c *Ctx) app(id string) (*apps.Definition, error) {
	def, err := apps.Get(id)
	if err != nil {
		return nil, failf(ExitUsage, "%v", err)
	}
	return def, nil
}

// runningProcs returns matching processes; it also reports when detection is
// impossible because the definition lists no process names.
func (c *Ctx) runningProcs(def *apps.Definition) ([]platform.ProcInfo, bool) {
	names := def.ProcessNames()
	if len(names) == 0 {
		return nil, false
	}
	procs, err := platform.Current().FindProcesses(names, def.Process.CmdlineContains)
	if err != nil {
		c.UI.Warnf("could not inspect running processes: %v", err)
		return nil, false
	}
	return procs, true
}

func (c *Ctx) refuserunning(def *apps.Definition, force bool) error {
	if force {
		return nil
	}
	procs, detectable := c.runningProcs(def)
	if !detectable {
		if !c.G.Yes {
			return failf(ExitRunning, "cannot reliably detect whether %s is running; close it and pass --yes to continue", def.Name)
		}
		return nil
	}
	if len(procs) > 0 {
		var b strings.Builder
		for i, p := range procs {
			if i == 3 {
				b.WriteString(", ...")
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (pid %d)", p.Name, p.PID)
		}
		return failf(ExitRunning, "%s is running: %s. Close it, or pass --force to continue at your own risk", def.Name, b.String())
	}
	return nil
}

func (c *Ctx) backupManager(vault string) *backup.Manager {
	dir := c.Cfg.EffectiveBackupDir(vault)
	return backup.NewManager(dir)
}

// guardForSet wires the live checks required by backup.Delete.
func (c *Ctx) guardForSet(vault string, s *backup.Set) backup.Guard {
	g := backup.Guard{
		JournalExists: func(app, target string) bool {
			return mover.JournalExists(vault, app, target)
		},
		ProcessAlive: platform.Current().IsProcessAlive,
	}
	g.ManifestHas = func(app, target string) (bool, error) {
		m, err := manifest.Load(vault)
		if err != nil {
			return false, err
		}
		i, _ := m.Find(app, target)
		return i >= 0, nil
	}
	g.LinkHealthy = func(original, _ string) (bool, string) {
		m, err := manifest.Load(vault)
		if err != nil {
			return false, "cannot read manifest: " + err.Error()
		}
		i, entry := m.Find(s.App, s.Target)
		if i < 0 {
			return true, "" // no entry: nothing to check
		}
		return mover.HealthCheck(entry.OriginalPath, entry.VaultPath)
	}
	return g
}

// stateLabel classifies a target for list/status.
func stateLabel(t targetInfo, tracked bool) string {
	switch {
	case t.Broken:
		return "broken-link"
	case t.IsLink && t.Moved:
		return "moved"
	case t.Partial || (tracked && !t.IsLink):
		return "partial"
	default:
		return "local"
	}
}

// dirStats returns size and file count, or zeroes when the folder is absent.
func dirStats(path string) (uint64, int) {
	if !mover.DirExists(path) {
		return 0, 0
	}
	b, f, err := mover.Measure(path)
	if err != nil {
		return 0, 0
	}
	return b, f
}

// ensureBackupLocation creates and validates the backup directory.
func (c *Ctx) ensureBackupLocation(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup directory %q: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".midas-write-test-*")
	if err != nil {
		return fmt.Errorf("backup directory %q is not writable: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return nil
}

// pathOverlaps reports whether one path is inside the other.
func pathOverlaps(a, b string) bool {
	return pathInside(a, b) || pathInside(b, a)
}

func pathInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
