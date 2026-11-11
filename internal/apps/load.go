package apps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	builtins "github.com/midas-cli/midas/apps"
	"github.com/midas-cli/midas/internal/platform"
)

// ErrNotFound is returned when no definition matches an app id.
var ErrNotFound = errors.New("app not found")

// Decode parses and structurally validates one definition. Unknown keys are
// rejected so typos in contributed files are caught.
func Decode(data []byte, source string) (*Definition, error) {
	var d Definition
	md, err := toml.Decode(string(data), &d)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown key(s): %s", source, strings.Join(keys, ", "))
	}
	d.Source = source
	if errs := d.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("%s: %w", source, errors.Join(errs...))
	}
	return &d, nil
}

// DecodeFile reads and decodes a definition file.
func DecodeFile(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data, path)
}

// Builtins loads the embedded definitions.
func Builtins() ([]*Definition, error) {
	entries, err := builtins.FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []*Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := builtins.FS.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		d, err := Decode(data, "builtin:"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// LocalDefinitionsDir returns the directory for user definitions.
func LocalDefinitionsDir() string {
	return filepath.Join(platform.Current().ConfigDir(), "apps")
}

// LoadLocal loads user definitions from dir. A missing directory is empty.
func LoadLocal(dir string) ([]*Definition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		d, err := DecodeFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// All returns built-ins merged with local definitions (local wins by id),
// sorted by id.
func All() ([]*Definition, error) {
	bi, err := Builtins()
	if err != nil {
		return nil, err
	}
	local, err := LoadLocal(LocalDefinitionsDir())
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*Definition, len(bi)+len(local))
	for _, d := range bi {
		byID[d.ID] = d
	}
	for _, d := range local {
		byID[d.ID] = d
	}
	out := make([]*Definition, 0, len(byID))
	for _, d := range byID {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get finds one definition by id.
func Get(id string) (*Definition, error) {
	defs, err := All()
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
}
