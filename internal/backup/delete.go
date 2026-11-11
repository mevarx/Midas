package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/midas-cli/midas/internal/platform"
)

// Guard supplies the live checks deletion must perform. Callers wire these
// from the manifest and mover packages; a nil check means "cannot verify".
type Guard struct {
	JournalExists func(app, target string) bool
	LinkHealthy   func(original, vaultPath string) (healthy bool, detail string)
	ManifestHas   func(app, target string) (bool, error)
	ProcessAlive  func(pid int32) bool
}

// DeleteOptions controls deletion.
type DeleteOptions struct {
	Guard       Guard
	SkipConfirm bool
	// Confirm must ask the user to type the app name. Delete refuses to
	// proceed when neither Confirm nor SkipConfirm is supplied.
	Confirm func(app, id string) (bool, error)
}

// ProtectedError means the set cannot be deleted in its current state.
// The CLI maps it to exit code 9.
type ProtectedError struct{ Reason string }

func (e *ProtectedError) Error() string { return e.Reason }

// Delete removes a backup set, enforcing every Section 9.2.3 rule first.
// It is the only exported deletion entry point in this package.
func (m *Manager) Delete(s *Set, opts DeleteOptions) error {
	if err := CheckDelete(s, opts.Guard); err != nil {
		return err
	}

	// Rule 4: typed confirmation.
	if !opts.SkipConfirm {
		if opts.Confirm == nil {
			return &ProtectedError{Reason: "deletion needs confirmation; refusing to delete without it"}
		}
		ok, err := opts.Confirm(s.App, s.ID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("deletion cancelled")
		}
	}

	// Rule 6: remove contents, BACKUP.json last.
	return removeTreeNoFollow(s.Dir)
}

// CheckDelete applies rules 1-3 without deleting anything. It is exported so
// `backup list` can explain why a set is not deletable.
func CheckDelete(s *Set, g Guard) error {
	if s == nil {
		return &ProtectedError{Reason: "no backup set given"}
	}
	if s.Dir == "" {
		return &ProtectedError{Reason: "backup set has no directory"}
	}

	switch s.State {
	case StateComplete, StateRolledBack, StateAbandoned:
		// deletable states; fall through to further checks
	case StateCreating, StateVerified, StateInUse:
		return &ProtectedError{Reason: fmt.Sprintf(
			"backup %s is %s; it cannot be deleted until its move is finished and healthy",
			s.ID, StateLabel(s.State))}
	default:
		return &ProtectedError{Reason: fmt.Sprintf("backup %s has unknown state %q; refusing to delete", s.ID, s.State)}
	}

	// Rule 2: no journal, no live LOCK.
	if g.JournalExists != nil && g.JournalExists(s.App, s.Target) {
		return &ProtectedError{Reason: fmt.Sprintf(
			"a move journal still exists for %s/%s; finish or roll it back first (`midas doctor`)", s.App, s.Target)}
	}
	if live, err := lockIsLive(s, g); err != nil {
		return &ProtectedError{Reason: err.Error()}
	} else if live {
		return &ProtectedError{Reason: fmt.Sprintf("backup %s is locked by a running move", s.ID)}
	}

	// Rule 3: for complete sets, the link must currently be healthy, unless
	// the manifest entry is gone because the user already ran restore.
	if s.State == StateComplete {
		if g.ManifestHas == nil || g.LinkHealthy == nil {
			return &ProtectedError{Reason: "cannot verify the moved target's health; refusing to delete a complete backup"}
		}
		has, err := g.ManifestHas(s.App, s.Target)
		if err != nil {
			return &ProtectedError{Reason: "cannot read manifest: " + err.Error()}
		}
		if has {
			ok, detail := g.LinkHealthy(s.OriginalPath, "")
			if !ok {
				return &ProtectedError{Reason: fmt.Sprintf(
					"the moved target is not healthy (%s); fix it before deleting its backup (`midas doctor`)", detail)}
			}
		}
	}
	return nil
}

func lockIsLive(s *Set, g Guard) (bool, error) {
	lock, err := s.Lock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		// An unreadable lock is treated as held: safest for user data.
		return true, fmt.Errorf("LOCK file of backup %s is unreadable: %v", s.ID, err)
	}
	if lock.PID == 0 {
		return true, nil
	}
	host, _ := os.Hostname()
	if lock.Hostname != "" && host != "" && lock.Hostname != host {
		return true, nil // another machine may still be writing it
	}
	if g.ProcessAlive == nil {
		return true, nil // cannot check: assume live
	}
	return g.ProcessAlive(lock.PID), nil
}

// removeTreeNoFollow deletes root depth-first using Lstat semantics, clearing
// read-only flags and never following links. Junctions are links, not
// directories: WalkDir does not descend into them (Go reports them as
// irregular files), so each is a single entry removed with os.Remove, which
// deletes the reparse point without touching the target. BACKUP.json is
// removed last.
func removeTreeNoFollow(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if _, isLink, lerr := platform.Current().ReadLink(root); lerr == nil && isLink {
		_ = os.Chmod(root, 0o600)
		return platform.Current().RemoveLink(root)
	} else if lerr != nil {
		return fmt.Errorf("inspect %q: %w", root, lerr)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		_ = os.Chmod(root, 0o600)
		return os.Remove(root)
	}

	var paths []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		if _, isLink, lerr := platform.Current().ReadLink(p); lerr != nil {
			return fmt.Errorf("inspect %q: %w", p, lerr)
		} else if isLink {
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))

	jsonPath := filepath.Join(root, "BACKUP.json")
	var jsonFile string
	for _, p := range paths {
		if p == root {
			continue
		}
		if filepath.Clean(p) == filepath.Clean(jsonPath) {
			jsonFile = p
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete %q: %w", p, err)
		}
	}
	if jsonFile != "" {
		if err := os.Remove(jsonFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete %q: %w", jsonFile, err)
		}
	}
	if err := os.Remove(root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// MarkState transitions a set and persists it.
func (s *Set) MarkState(state string) error {
	s.State = state
	return s.Save()
}

// MarkAbandoned is used when a move is cancelled before anything changed.
func (s *Set) MarkAbandoned() error { return s.MarkState(StateAbandoned) }

// MarkInUse is set while the move is executing.
func (s *Set) MarkInUse() error { return s.MarkState(StateInUse) }

// MarkComplete is set after the link passed its health check.
func (s *Set) MarkComplete() error { return s.MarkState(StateComplete) }

// MarkRolledBack is set after the set restored the original state.
func (s *Set) MarkRolledBack() error { return s.MarkState(StateRolledBack) }

// Age returns how long ago the set was created.
func (s *Set) Age() time.Duration {
	if s.CreatedAt.IsZero() {
		return 0
	}
	return time.Since(s.CreatedAt)
}
