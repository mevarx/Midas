package apps

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// osTokens mirrors the PathTokens maps in internal/platform for each
// supported OS, with a fixed synthetic home, so built-in definitions can be
// expanded and checked on every OS from any host.
func osTokens(goos string) map[string]string {
	switch goos {
	case "windows":
		home := `C:\Users\tester`
		appdata := home + `\AppData\Roaming`
		local := home + `\AppData\Local`
		return map[string]string{
			"home":         home,
			"appdata":      appdata,
			"localappdata": local,
			"xdg_config":   appdata,
			"xdg_data":     local,
			"xdg_cache":    local + `\cache`,
			"xdg_state":    local,
		}
	case "darwin":
		home := `/Users/tester`
		appSupport := home + `/Library/Application Support`
		return map[string]string{
			"home":         home,
			"appdata":      appSupport,
			"localappdata": appSupport,
			"xdg_config":   appSupport,
			"xdg_data":     appSupport,
			"xdg_cache":    home + `/Library/Caches`,
			"xdg_state":    appSupport,
		}
	default: // linux
		home := `/home/tester`
		cfg := home + `/.config`
		return map[string]string{
			"home":         home,
			"appdata":      cfg,
			"localappdata": cfg,
			"xdg_config":   cfg,
			"xdg_data":     home + `/.local/share`,
			"xdg_cache":    home + `/.cache`,
			"xdg_state":    home + `/.local/state`,
		}
	}
}

// overrideVars are the relocation env vars shipped definitions honour via
// ${VAR:-default}. Every entry is documented by the vendor (see
// docs/RESEARCH.md); the registry tests clear them so expansion is
// deterministic regardless of the host environment.
var overrideVars = []string{
	"CLAUDE_CONFIG_DIR",
	"CODEX_HOME",
	"GEMINI_CLI_HOME",
	"HERMES_HOME",
	"QWEN_HOME",
	"COPILOT_HOME",
	"COPILOT_CACHE_HOME",
	"CRUSH_GLOBAL_CONFIG",
	"CRUSH_GLOBAL_DATA",
	"CRUSH_CACHE_DIR",
	"AMP_HOME",
	"CURSOR_DATA_DIR",
	"CLINE_DIR",
	"CONTINUE_GLOBAL_DIR",
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"XDG_CACHE_HOME",
	"XDG_STATE_HOME",
}

var envDefaultRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):-`)

// normPath makes a path comparable across host/target OS pairs by forcing
// forward slashes (a Linux host keeps Windows backslashes verbatim).
func normPath(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), `\`, `/`)
}

// goldenPaths pins the expanded default location of every built-in target
// on every OS, relative to the synthetic home, in windows/darwin/linux
// order. Values come from vendor docs or path-resolution source code,
// recorded with citations in docs/RESEARCH.md (snapshot 2026-11-19). An
// empty string means the target intentionally does not exist on that OS.
//
// This is the gate that keeps the registry honest: a definition change that
// moves a default path must update this table and the research record
// together, so a silent regression cannot ship.
var goldenPaths = map[string]map[string][3]string{
	"claude-code": {
		"data": {".claude", ".claude", ".claude"},
	},
	"opencode": {
		// Raw XDG semantics on every OS, Windows included: no APPDATA,
		// no Library/Application Support.
		"data":   {".local/share/opencode", ".local/share/opencode", ".local/share/opencode"},
		"config": {".config/opencode", ".config/opencode", ".config/opencode"},
		"cache":  {".cache/opencode", ".cache/opencode", ".cache/opencode"},
		"state":  {".local/state/opencode", ".local/state/opencode", ".local/state/opencode"},
	},
	"hermes": {
		"data": {"AppData/Local/hermes", ".hermes", ".hermes"},
	},
	"codex": {
		"data": {".codex", ".codex", ".codex"},
	},
	"gemini-cli": {
		"data": {".gemini", ".gemini", ".gemini"},
	},
	"copilot-cli": {
		"data":  {".copilot", ".copilot", ".copilot"},
		"cache": {"AppData/Local/copilot", "Library/Caches/copilot", ".cache/copilot"},
	},
	"qwen-code": {
		"data": {".qwen", ".qwen", ".qwen"},
	},
	"iflow": {
		"data": {".iflow", ".iflow", ".iflow"},
	},
	"aider": {
		"data": {".aider", ".aider", ".aider"},
	},
	"goose": {
		"config": {"AppData/Roaming/Block/goose/config", ".config/goose", ".config/goose"},
		"data":   {"AppData/Roaming/Block/goose/data", ".local/share/goose", ".local/share/goose"},
		// On Windows the etcetera state dir falls back to the data dir,
		// so no separate state target exists there.
		"state": {"", ".local/state/goose", ".local/state/goose"},
		"cache": {"AppData/Local/Block/goose/cache", ".cache/goose", ".cache/goose"},
	},
	"crush": {
		// Crush uses ~/.config on every OS, including Windows.
		"config": {".config/crush", ".config/crush", ".config/crush"},
		"data":   {"AppData/Local/crush", ".local/share/crush", ".local/share/crush"},
		"cache":  {"AppData/Local/crush/cache", ".cache/crush", ".cache/crush"},
	},
	"amp": {
		"data":      {".local/share/amp", ".local/share/amp", ".local/share/amp"},
		"config":    {".config/amp", ".config/amp", ".config/amp"},
		"installer": {".amp", ".amp", ".amp"},
	},
	"droid": {
		"data": {".factory", ".factory", ".factory"},
	},
	"cursor": {
		"user-data": {"AppData/Roaming/Cursor", "Library/Application Support/Cursor", ".config/Cursor"},
		"cli":       {".cursor", ".cursor", ".cursor"},
		// macOS keeps the sync stores inside Application Support, so
		// they move with user-data; Windows and Linux keep them apart.
		"agent-stores": {"AppData/Local/Cursor/AgentStores", "", ".local/state/cursor/agent-stores"},
	},
	"windsurf": {
		"cascade":         {".codeium/windsurf", ".codeium/windsurf", ".codeium/windsurf"},
		"user-data":       {"AppData/Roaming/Windsurf", "Library/Application Support/Windsurf", ".config/Windsurf"},
		"devin-user-data": {"AppData/Roaming/Devin", "Library/Application Support/Devin", ".config/Devin"},
	},
	"zed": {
		"data":   {"AppData/Local/Zed", "Library/Application Support/Zed", ".local/share/zed"},
		"config": {"AppData/Roaming/Zed", ".config/zed", ".config/zed"},
	},
	"cline": {
		"data": {".cline", ".cline", ".cline"},
		"vscode-legacy": {
			"AppData/Roaming/Code/User/globalStorage/saoudrizwan.claude-dev",
			"Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev",
			".config/Code/User/globalStorage/saoudrizwan.claude-dev",
		},
	},
	"roo-code": {
		"vscode": {
			"AppData/Roaming/Code/User/globalStorage/rooveterinaryinc.roo-cline",
			"Library/Application Support/Code/User/globalStorage/rooveterinaryinc.roo-cline",
			".config/Code/User/globalStorage/rooveterinaryinc.roo-cline",
		},
		"config": {".roo", ".roo", ".roo"},
		"cli":    {".vscode-mock", ".vscode-mock", ".vscode-mock"},
	},
	"continue": {
		"data": {".continue", ".continue", ".continue"},
	},
}

// TestBuiltinGoldenPaths checks every built-in target against the researched
// default locations, and requires every built-in to have a golden entry so
// new definitions cannot skip the research gate.
func TestBuiltinGoldenPaths(t *testing.T) {
	for _, v := range overrideVars {
		t.Setenv(v, "")
	}
	defs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	osIndex := map[string]int{"windows": 0, "darwin": 1, "linux": 2}
	seen := map[string]bool{}
	for _, d := range defs {
		seen[d.ID] = true
		golden, ok := goldenPaths[d.ID]
		if !ok {
			t.Errorf("%s: no golden path entry; research the defaults and add them to goldenPaths and docs/RESEARCH.md", d.ID)
			continue
		}
		for _, tgt := range d.Targets {
			want, ok := golden[tgt.Name]
			if !ok {
				t.Errorf("%s: target %q has no golden entry", d.ID, tgt.Name)
				continue
			}
			for goos, idx := range osIndex {
				tokens := osTokens(goos)
				home := normPath(tokens["home"])
				raw := tgt.PathForOSE(goos)
				if want[idx] == "" {
					if raw != "" {
						t.Errorf("%s target %q on %s: defined as %q but research says it does not exist there", d.ID, tgt.Name, goos, raw)
					}
					continue
				}
				if raw == "" {
					t.Errorf("%s target %q on %s: missing, want %q", d.ID, tgt.Name, goos, home+"/"+want[idx])
					continue
				}
				exp, err := expandWithTokens(raw, tokens)
				if err != nil {
					t.Errorf("%s target %q on %s: expand: %v", d.ID, tgt.Name, goos, err)
					continue
				}
				if got := normPath(exp); got != home+"/"+want[idx] {
					t.Errorf("%s target %q on %s: got %q, want %q", d.ID, tgt.Name, goos, got, home+"/"+want[idx])
				}
			}
		}
	}
	for id := range goldenPaths {
		if !seen[id] {
			t.Errorf("goldenPaths lists %q but no built-in definition exists", id)
		}
	}
}

// TestBuiltinRegistry validates every embedded definition structurally and
// expands every target for every OS it declares, using synthetic tokens.
// It is the gate that keeps contributed built-ins shippable on all platforms.
func TestBuiltinRegistry(t *testing.T) {
	for _, v := range overrideVars {
		t.Setenv(v, "")
	}
	defs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no built-in definitions embedded")
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if seen[d.ID] {
			t.Errorf("duplicate built-in id %q", d.ID)
		}
		seen[d.ID] = true
		if errs := d.Validate(); len(errs) > 0 {
			for _, e := range errs {
				t.Errorf("%s: %v", d.ID, e)
			}
			continue
		}
		for _, goos := range []string{"windows", "linux", "darwin"} {
			tokens := osTokens(goos)
			home := normPath(tokens["home"])
			for _, tgt := range d.Targets {
				raw := tgt.PathForOSE(goos)
				if raw == "" {
					continue // not defined for this OS
				}
				exp, err := expandWithTokens(raw, tokens)
				if err != nil {
					t.Errorf("%s/%s target %q: expand on %s: %v", d.ID, goos, tgt.Name, goos, err)
					continue
				}
				got := normPath(exp)
				if !strings.HasPrefix(got, home+"/") {
					t.Errorf("%s target %q on %s: %q is outside synthetic home %q", d.ID, tgt.Name, goos, got, home)
				}
			}
		}
	}
}

// TestBuiltinEnvOverrides proves that every ${VAR:-default} override in the
// registry actually redirects expansion when the variable is set, on every
// OS the target declares.
func TestBuiltinEnvOverrides(t *testing.T) {
	for _, v := range overrideVars {
		t.Setenv(v, "")
	}
	defs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	for _, d := range defs {
		for _, goos := range []string{"windows", "linux", "darwin"} {
			tokens := osTokens(goos)
			for _, tgt := range d.Targets {
				raw := tgt.PathForOSE(goos)
				if raw == "" {
					continue
				}
				for _, m := range envDefaultRe.FindAllStringSubmatch(raw, -1) {
					v := m[1]
					custom := filepath.Join(tokens["home"], "midas-override", strings.ToLower(v))
					t.Setenv(v, custom)
					exp, err := expandWithTokens(raw, tokens)
					if err != nil {
						t.Errorf("%s target %q on %s with %s set: %v", d.ID, tgt.Name, goos, v, err)
					} else if !strings.HasPrefix(normPath(exp), normPath(custom)) {
						t.Errorf("%s target %q on %s: %s=%q ignored, got %q", d.ID, tgt.Name, goos, v, custom, exp)
					}
					t.Setenv(v, "")
				}
			}
		}
	}
}

// TestBuiltinHomeContainmentOnHost runs the real host validation path for
// every built-in, the same check `midas apps validate` performs.
func TestBuiltinHomeContainmentOnHost(t *testing.T) {
	for _, v := range overrideVars {
		t.Setenv(v, "")
	}
	defs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	for _, d := range defs {
		if errs := ValidateForHost(d, ""); len(errs) > 0 {
			for _, e := range errs {
				t.Errorf("%s: %v", d.ID, e)
			}
		}
	}
}
