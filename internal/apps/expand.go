package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mevarx/Midas/internal/platform"
)

var (
	tokenRe = regexp.MustCompile(`\{([a-z_]+)\}`)
	winEnv  = regexp.MustCompile(`%([^%]+)%`)
	// shEnv matches $VAR, ${VAR} and ${VAR:-default}. The default is used
	// when VAR is unset or empty, matching shell semantics; it may contain
	// ~ and {token} placeholders (tokens are expanded before env vars) but
	// not nested env references or a literal '}'.
	shEnv = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// ExpandPath expands ~, environment variables and {token} placeholders.
// Env references support $VAR, ${VAR}, %VAR% and ${VAR:-default}; a
// referenced variable that is unset or empty without a default is an error,
// never a silently empty path segment. The result is cleaned but not made
// absolute; callers validate absoluteness.
func ExpandPath(raw string) (string, error) {
	return expandWithTokens(raw, platform.Current().PathTokens())
}

// expandWithTokens is ExpandPath with an explicit token set, so definitions
// can be expanded and tested for any OS, not just the host.
func expandWithTokens(raw string, tokens map[string]string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty path")
	}
	s := raw
	var missingEnv []string

	s = tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(m, "{"), "}")
		if v, ok := tokens[name]; ok && v != "" {
			return v
		}
		return m
	})
	s = winEnv.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		v := os.Getenv(name)
		if v == "" {
			missingEnv = append(missingEnv, name)
		}
		return v
	})
	s = shEnv.ReplaceAllStringFunc(s, func(m string) string {
		sub := shEnv.FindStringSubmatch(m)
		name, fallback := sub[1], sub[2]
		if name == "" {
			name = sub[3] // bare $VAR form
		}
		v := os.Getenv(name)
		if v == "" {
			v = fallback
		}
		if v == "" {
			missingEnv = append(missingEnv, name)
		}
		return v
	})
	if len(missingEnv) > 0 {
		return "", fmt.Errorf("environment variable(s) not set or empty: %s (use ${VAR:-default} to supply a fallback)", strings.Join(missingEnv, ", "))
	}

	home := tokens["home"]
	if s == "~" {
		s = home
	} else if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		s = filepath.Join(home, s[2:])
	}
	if strings.Contains(s, "{") || strings.Contains(s, "$") || strings.Contains(s, "%") {
		return "", fmt.Errorf("unresolved placeholder in path %q", raw)
	}
	return filepath.Clean(filepath.FromSlash(s)), nil
}

// ValidateTargetPath enforces the target path safety rules.
func ValidateTargetPath(p, home, vault string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path %q is not absolute after expansion", p)
	}
	if err := checkDangerous(p, home, vault); err != nil {
		return err
	}
	if !isUnder(p, home) {
		return fmt.Errorf("path %q is outside the user's home directory (%s)", p, home)
	}
	if err := validateResolvedContainment(p, home); err != nil {
		return err
	}
	return nil
}

func validateResolvedContainment(path, home string) error {
	realHome, err := platform.Current().ResolvePath(home)
	if err != nil {
		return fmt.Errorf("resolve home directory %q: %w", home, err)
	}
	resolved, err := platform.Current().ResolvePath(path)
	if err != nil {
		return fmt.Errorf("resolve target path %q: %w", path, err)
	}
	if !isUnder(resolved, realHome) && !platform.SamePath(resolved, realHome) {
		return fmt.Errorf("path %q resolves outside the user's home directory", path)
	}
	return nil
}

func checkDangerous(p, home, vault string) error {
	clean := filepath.Clean(p)
	if platform.IsVolumeRoot(clean) {
		return fmt.Errorf("refusing to operate on filesystem root %q", clean)
	}
	if platform.SamePath(clean, home) {
		return fmt.Errorf("refusing to operate on the home directory itself (%q)", clean)
	}
	if vault != "" && platform.SamePath(clean, vault) {
		return fmt.Errorf("refusing to operate on the vault itself (%q)", clean)
	}
	for _, sys := range platform.SystemDirs() {
		if platform.SamePath(clean, sys) || isUnder(clean, sys) {
			return fmt.Errorf("refusing to operate on system directory %q", clean)
		}
	}
	return nil
}

// isUnder reports whether child is strictly inside parent.
func isUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ValidateForHost checks structural rules plus the paths for this OS.
func ValidateForHost(d *Definition, vault string) []error {
	errs := d.Validate()
	home, err := platform.HomeDir()
	if err != nil {
		return append(errs, err)
	}
	for _, t := range d.Targets {
		raw := t.Path()
		if raw == "" {
			continue // not defined for this OS: skipped, not an error
		}
		exp, err := ExpandPath(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("target %q: %w", t.Name, err))
			continue
		}
		if err := ValidateTargetPath(exp, home, vault); err != nil {
			errs = append(errs, fmt.Errorf("target %q: %w", t.Name, err))
		}
	}
	return errs
}

// ExpandTarget returns the absolute target path for this OS.
func ExpandTarget(t Target) (string, error) {
	raw := t.Path()
	if raw == "" {
		return "", fmt.Errorf("target %q has no path for %s", t.Name, platform.OS())
	}
	return ExpandPath(raw)
}
