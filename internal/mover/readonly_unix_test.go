//go:build !windows

package mover

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
)

// makeReadOnlyTree builds a tree whose permissions cannot be written to by the
// owner: a 0555 directory with a file, a 0500 subdirectory with a file, and an
// ordinary writable directory and file. Mkdir is affected by umask, so every
// mode is applied explicitly with os.Chmod after creation.
func makeReadOnlyTree(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() { chmodTreeWritable(root) })
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(root, "readonly"), 0o555},
		{filepath.Join(root, "readonly", "nested"), 0o500},
		{filepath.Join(root, "writable"), 0o755},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(root, "top.txt"), 0o644},
		{filepath.Join(root, "readonly", "inside.txt"), 0o644},
		{filepath.Join(root, "readonly", "nested", "deep.txt"), 0o600},
		{filepath.Join(root, "writable", "ordinary.txt"), 0o640},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte("data for "+filepath.Base(f.path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Files first, directories last and deepest first, so a read-only parent
	// never blocks applying the child's mode.
	for _, f := range files {
		if err := os.Chmod(f.path, f.mode); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			t.Fatal(err)
		}
	}
}

// chmodTreeWritable makes every directory in root traversable again so
// t.TempDir cleanup can delete it; errors are expected once a tree is gone.
func chmodTreeWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
}

// assertTreeModesMatch checks that every non-link entry in dst, including the
// root itself, has the permission bits of its counterpart in src.
func assertTreeModesMatch(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if _, isLink, lerr := platform.Current().ReadLink(p); lerr != nil {
			return lerr
		} else if isLink {
			return nil
		}
		want, ierr := os.Lstat(p)
		if ierr != nil {
			return ierr
		}
		dstPath := dst
		if rel != "." {
			dstPath = filepath.Join(dst, rel)
		}
		got, ierr := os.Lstat(dstPath)
		if ierr != nil {
			return ierr
		}
		if got.Mode().Perm() != want.Mode().Perm() {
			t.Errorf("%q mode = %04o, want %04o", dstPath, got.Mode().Perm(), want.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestStagedCopyOfReadOnlyTreePreservesModes is the Unix regression: a source
// tree containing read-only directories cannot be recreated file by file unless
// the staged copy is writable, and the final modes must be restored afterwards.
func TestStagedCopyOfReadOnlyTreePreservesModes(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "source")
	dst := filepath.Join(base, "staged")
	makeReadOnlyTree(t, src)

	if _, err := CopyTree(src, dst, CopyOptions{StageWritable: true}); err != nil {
		t.Fatalf("staged copy of a read-only tree failed: %v", err)
	}
	t.Cleanup(func() { chmodTreeWritable(dst) })

	// Before the modes are restored, every staged directory must be writable
	// so later stages (and cleanup) can operate on it.
	err := filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if info.Mode().Perm()&0o200 == 0 {
			t.Errorf("staged directory %q is not writable: %04o", p, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := applyTreeModes(src, dst); err != nil {
		t.Fatalf("applyTreeModes: %v", err)
	}
	assertTreeModesMatch(t, src, dst)
	if _, err := VerifyTree(src, dst, true); err != nil {
		t.Fatalf("verify staged copy: %v", err)
	}
}

// TestRemoveOwnedPathRemovesReadOnlyTree is the cleanup regression: children of
// read-only directories cannot be unlinked until the owner-write bit is set.
func TestRemoveOwnedPathRemovesReadOnlyTree(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "readonly.midas-partial")
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkOwnedPath(parent, path, id, "move-partial"); err != nil {
		t.Fatal(err)
	}
	makeReadOnlyTree(t, path)

	if err := RemoveOwnedPath(parent, path, id, "move-partial"); err != nil {
		t.Fatalf("RemoveOwnedPath on a read-only tree: %v", err)
	}
	if PathExists(path) || PathExists(ownerPath(path)) {
		t.Fatal("read-only owned path or its ownership marker still exists")
	}
}

// TestPlaceFromVaultStagingSequencePreservesModes covers the staging path used
// by placeFromVault when the vault copy is on another volume. That branch
// cannot be forced on a single-volume test runner (the first os.Rename would
// succeed), so this test runs the same helper sequence directly: staged
// writable copy, verify, restore modes, then rename into place.
func TestPlaceFromVaultStagingSequencePreservesModes(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "vault-copy")
	source := filepath.Join(base, "restored")
	makeReadOnlyTree(t, dest)

	partial := source + ".midas-partial"
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkOwnedPath(filepath.Dir(partial), partial, id, "rollback-partial"); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyTree(dest, partial, CopyOptions{Context: context.Background(), Workers: 2, StageWritable: true}); err != nil {
		t.Fatalf("stage rollback copy: %v", err)
	}
	t.Cleanup(func() { chmodTreeWritable(partial) })
	if _, err := VerifyTree(dest, partial, true); err != nil {
		t.Fatalf("verify staged rollback copy: %v", err)
	}
	if err := applyTreeModes(dest, partial); err != nil {
		t.Fatalf("applyTreeModes: %v", err)
	}
	if err := os.Rename(partial, source); err != nil {
		t.Fatalf("commit staged rollback copy: %v", err)
	}
	t.Cleanup(func() { chmodTreeWritable(source) })
	if err := ForgetOwnership(filepath.Dir(partial), partial, id, "rollback-partial"); err != nil {
		t.Fatalf("clear rollback ownership: %v", err)
	}

	assertTreeModesMatch(t, dest, source)
	if got, err := os.ReadFile(filepath.Join(source, "readonly", "nested", "deep.txt")); err != nil || len(got) == 0 {
		t.Fatalf("restored read-only file unreadable: %q, %v", got, err)
	}
}

// TestRestoreReadOnlyVaultTreeKeepsOriginalModes runs the cross-volume restore
// copy branch end to end: KeepVaultCopy forces that branch even when the vault
// and the original location share a volume, which a single-volume test runner
// cannot otherwise reach.
func TestRestoreReadOnlyVaultTreeKeepsOriginalModes(t *testing.T) {
	base := t.TempDir()
	vault := filepath.Join(base, "vault")
	dest := filepath.Join(vault, "apps", "example", "data")
	source := filepath.Join(base, "home", "data")
	makeReadOnlyTree(t, dest)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { chmodTreeWritable(source) })
	partial := source + ".midas-partial"
	t.Cleanup(func() { chmodTreeWritable(partial) })

	m := manifest.Empty()
	m.Upsert(manifest.Entry{
		App:          "example",
		Target:       "data",
		OriginalPath: source,
		VaultPath:    dest,
		MovedAt:      time.Now().UTC(),
	})
	if err := Restore(RestoreOptions{
		App: "example", Target: "data", Source: source, Dest: dest, Vault: vault,
		Manifest: m, Verify: "hash", KeepVaultCopy: true,
	}); err != nil {
		t.Fatalf("restore of a read-only vault tree: %v", err)
	}

	assertTreeModesMatch(t, dest, source)
	for _, rel := range []string{
		"top.txt",
		filepath.Join("readonly", "inside.txt"),
		filepath.Join("readonly", "nested", "deep.txt"),
		filepath.Join("writable", "ordinary.txt"),
	} {
		got, err := os.ReadFile(filepath.Join(source, rel))
		if err != nil {
			t.Fatalf("restored %q unreadable: %v", rel, err)
		}
		want, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("vault copy %q unreadable: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("restored %q = %q, want %q", rel, got, want)
		}
	}
	assertRealDirectory(t, dest, "vault copy after keep-vault-copy restore")
	if PathExists(partial) || PathExists(ownerPath(partial)) {
		t.Fatal("restore left the staging path or its ownership marker")
	}
	if idx, _ := m.Find("example", "data"); idx >= 0 {
		t.Fatal("restore left a manifest entry")
	}
	assertNoJournal(t, vault, "example", "data")
}
