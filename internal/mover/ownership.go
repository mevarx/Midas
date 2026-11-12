package mover

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mevarx/Midas/internal/platform"
)

const ownershipSchema = 1

type ownershipRecord struct {
	Schema      int    `json:"schema"`
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	Path        string `json:"path"`
}

// NewOperationID creates a random identifier suitable for journals and
// ownership records.
func NewOperationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("create operation ID: %w", err)
	}
	return "op-" + hex.EncodeToString(b), nil
}

func ownerPath(path string) string { return path + ".midas-owner.json" }

// EnsureVaultAppDir creates the expected app directory without accepting
// symlink substitutions inside the vault.
func EnsureVaultAppDir(vault, app string) error {
	if !validJournalComponent(app) {
		return fmt.Errorf("invalid app path component %q", app)
	}
	root, err := filepath.Abs(filepath.Clean(vault))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	realRoot, err := platform.Current().ResolvePath(root)
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(root, "apps"), filepath.Join(root, "apps", app)} {
		fi, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			fi, err = os.Lstat(dir)
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(dir) {
			return fmt.Errorf("vault app path %q is not a real directory", dir)
		}
		resolved, err := platform.Current().ResolvePath(dir)
		if err != nil {
			return err
		}
		if inside, _ := pathWithinRoot(realRoot, resolved); !inside {
			return fmt.Errorf("vault app path %q resolves outside the vault", dir)
		}
	}
	return nil
}

func validOperationID(id string) bool {
	if !strings.HasPrefix(id, "op-") || len(id) != 35 {
		return false
	}
	_, err := hex.DecodeString(id[3:])
	return err == nil
}

// ValidOperationID reports whether id is in the generated operation-ID format.
func ValidOperationID(id string) bool { return validOperationID(id) }

func directChild(root, path string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is not contained in %q", path, root)
	}
	if strings.ContainsAny(rel, `/\\`) || rel == "." || rel == ".." {
		return "", fmt.Errorf("path %q must be a direct child of %q", path, root)
	}
	return path, nil
}

// MarkOwnedPath writes exclusive metadata before Midas creates a scratch path.
func MarkOwnedPath(root, path, operationID, kind string) error {
	path, err := directChild(root, path)
	if err != nil {
		return err
	}
	if !validOperationID(operationID) || kind == "" {
		return errors.New("invalid operation ownership metadata")
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("refusing to claim existing path %q", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record := ownershipRecord{Schema: ownershipSchema, OperationID: operationID, Kind: kind, Path: path}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(ownerPath(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("claim path %q: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// MarkExistingOwnedPath claims an existing real directory for a cleanup that
// has already passed its higher-level safety checks.
func MarkExistingOwnedPath(root, path, operationID, kind string) error {
	path, err := directChild(root, path)
	if err != nil {
		return err
	}
	if !validOperationID(operationID) || kind == "" {
		return errors.New("invalid operation ownership metadata")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(path) {
		return fmt.Errorf("refusing to claim non-directory path %q", path)
	}
	record := ownershipRecord{Schema: ownershipSchema, OperationID: operationID, Kind: kind, Path: path}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(ownerPath(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("claim path %q: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func validateOwnership(root, path, operationID, kind string) error {
	path, err := directChild(root, path)
	if err != nil {
		return err
	}
	if !validOperationID(operationID) {
		return errors.New("invalid operation ID; refusing cleanup")
	}
	marker := ownerPath(path)
	fi, err := os.Lstat(marker)
	if err != nil {
		return fmt.Errorf("ownership metadata for %q is unavailable; preserving it: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("ownership metadata for %q is not a regular file; preserving it", path)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return fmt.Errorf("read ownership metadata for %q: %w", path, err)
	}
	var record ownershipRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("ownership metadata for %q is corrupt; preserving it: %w", path, err)
	}
	if record.Schema != ownershipSchema || record.OperationID != operationID || record.Kind != kind || filepath.Clean(record.Path) != path {
		return fmt.Errorf("ownership metadata does not match operation %s and path %q; preserving it", operationID, path)
	}
	return nil
}

// ValidateOwnedPath confirms that a path's metadata matches the expected
// operation. It does not modify the path.
func ValidateOwnedPath(root, path, operationID, kind string) error {
	return validateOwnership(root, path, operationID, kind)
}

// ForgetOwnership removes a validated marker after the owned path is renamed
// into its committed location.
func ForgetOwnership(root, path, operationID, kind string) error {
	path, err := directChild(root, path)
	if err != nil {
		return err
	}
	if err := validateOwnership(root, path, operationID, kind); err != nil {
		return err
	}
	return os.Remove(ownerPath(path))
}

// RemoveOwnedPath removes a path only when its operation metadata matches.
// Lstat and SameFile checks ensure links or replaced paths are not traversed.
func RemoveOwnedPath(root, path, operationID, kind string) error {
	path, err := directChild(root, path)
	if err != nil {
		return err
	}
	if err := validateOwnership(root, path, operationID, kind); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.Remove(ownerPath(path))
	}
	if err != nil {
		return err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || !platform.IsRealDirectory(path) {
		return fmt.Errorf("owned path %q is not a real directory; preserving it", path)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return fmt.Errorf("owned path %q changed during validation; preserving it", path)
	}
	if err := removeOwnedTree(path); err != nil {
		return fmt.Errorf("remove owned path %q: %w", path, err)
	}
	return os.Remove(ownerPath(path))
}

// removeOwnedTree deletes an owned tree depth-first using Lstat semantics,
// clearing read-only modes first so children of read-only directories can be
// unlinked. Links and junctions are removed as single entries and never
// followed. The ownership marker next to path is not touched here.
func removeOwnedTree(path string) error {
	var paths []string
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
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
			if cerr := os.Chmod(p, 0o700); cerr != nil && !errors.Is(cerr, os.ErrNotExist) {
				return fmt.Errorf("clear read-only mode on %q: %w", p, cerr)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, p := range paths {
		if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return fmt.Errorf("delete %q: %w", p, rerr)
		}
	}
	return nil
}
