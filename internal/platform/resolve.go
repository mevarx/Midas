package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveViaReadLink walks path component by component, expanding every link
// (symlinks on Unix, symlinks and junctions on Windows) via the platform
// ReadLink. A missing leaf is tolerated: resolution stops at the first
// component that does not exist and joins the remainder verbatim. Loops and
// excess expansions are rejected.
func resolveViaReadLink(r platformReadLinker, path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	vol := filepath.VolumeName(abs)
	rest := strings.TrimPrefix(abs, vol)
	rest = strings.TrimPrefix(rest, string(filepath.Separator))
	var components []string
	if rest != "" {
		components = strings.Split(rest, string(filepath.Separator))
	}
	resolved := vol + string(filepath.Separator)
	if vol == "" {
		resolved = string(filepath.Separator)
	}
	const maxExpansions = 255
	expansions := 0
	for i := 0; i < len(components); i++ {
		name := components[i]
		if name == "" || name == "." {
			continue
		}
		if name == ".." {
			resolved = filepath.Dir(resolved)
			continue
		}
		candidate := filepath.Join(resolved, name)
		fi, err := os.Lstat(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return filepath.Join(append([]string{resolved}, components[i:]...)...), nil
			}
			return "", fmt.Errorf("resolve %q: %w", path, err)
		}
		_ = fi
		target, isLink, err := r.ReadLink(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", path, err)
		}
		if !isLink {
			resolved = candidate
			continue
		}
		expansions++
		if expansions > maxExpansions {
			return "", fmt.Errorf("resolve %q: too many links (loop?)", path)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(resolved, target)
		}
		targetAbs, err := filepath.Abs(filepath.Clean(target))
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", path, err)
		}
		tVol := filepath.VolumeName(targetAbs)
		tRest := strings.TrimPrefix(targetAbs, tVol)
		tRest = strings.TrimPrefix(tRest, string(filepath.Separator))
		var tComps []string
		if tRest != "" {
			tComps = strings.Split(tRest, string(filepath.Separator))
		}
		resolved = tVol + string(filepath.Separator)
		if tVol == "" {
			resolved = string(filepath.Separator)
		}
		components = append(tComps, components[i+1:]...)
		i = -1
	}
	return filepath.Clean(resolved), nil
}

// platformReadLinker is the ReadLink half of Platform, so the shared
// resolver can run on either implementation.
type platformReadLinker interface {
	ReadLink(path string) (target string, isLink bool, err error)
}
