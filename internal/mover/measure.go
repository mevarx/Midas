package mover

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mevarx/Midas/internal/platform"
)

// Measure walks a directory without following links and returns its logical
// size and file count.
func Measure(root string) (uint64, int, error) {
	var bytes uint64
	var files int
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		_, isLink, lerr := platform.Current().ReadLink(p)
		if lerr != nil {
			return fmt.Errorf("read link %q: %w", p, lerr)
		}
		if isLink {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		bytes += uint64(info.Size())
		files++
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("measure %q: %w", root, err)
	}
	return bytes, files, nil
}

// DirExists reports whether path is an existing directory (following links).
func DirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
