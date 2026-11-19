package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/config"
	"github.com/mevarx/Midas/internal/ui"
)

// syntheticHome points every home/appdata/XDG variable at an empty temp
// tree so list scans no real data, on any host OS. Setting all of them is
// harmless: each OS reads only its own. It also neutralizes every
// relocation override the built-in definitions reference (e.g. HERMES_HOME
// is set inside a Hermes session), so an override cannot point the scan at
// real multi-GB data. The override list is derived from the definitions
// themselves, so new built-ins are covered automatically.
func syntheticHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range []string{
		"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
	} {
		t.Setenv(v, filepath.Join(root, strings.ToLower(v)))
	}
	defs, err := apps.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	refRe := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-[^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)|%([^%]+)%`)
	for _, d := range defs {
		for _, tgt := range d.Targets {
			for _, raw := range []string{tgt.Windows, tgt.Linux, tgt.Darwin} {
				for _, m := range refRe.FindAllStringSubmatch(raw, -1) {
					v := m[1]
					if v == "" {
						v = m[2]
					}
					if v == "" {
						v = m[3]
					}
					t.Setenv(v, filepath.Join(root, "overrides", strings.ToLower(v)))
				}
			}
		}
	}
	return root
}

// TestListWithoutVaultShowsInstalledApps proves `midas list` works before
// `midas init`: discovery mode lists installed apps with an empty manifest
// and warns, instead of failing with a usage error.
func TestListWithoutVaultShowsInstalledApps(t *testing.T) {
	syntheticHome(t)
	var stdout, stderr bytes.Buffer
	ctx := &Ctx{
		G:   Global{JSON: true},
		Cfg: &config.Config{Backup: "none", Verify: "size"},
		UI:  &ui.UI{Out: &stdout, Err: &stderr, JSON: true},
	}
	if err := ctx.cmdList([]string{"--json"}); err != nil {
		t.Fatalf("list without vault: %v", err)
	}
	var rows []listRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("list output is not a JSON row array: %v\n%s", err, stdout.String())
	}
	found := false
	for _, r := range rows {
		if r.App == "claude-code" && r.Target == "data" {
			found = true
		}
		if r.State == "moved" {
			t.Errorf("discovery mode reported a moved target without a manifest: %+v", r)
		}
	}
	if !found {
		t.Fatalf("built-in claude-code missing from discovery list: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no vault configured") {
		t.Fatalf("discovery warning missing from stderr: %s", stderr.String())
	}
}

// TestListWithUnreachableVaultStillFails proves discovery mode never masks a
// configured but missing vault: that stays a hard exit-5 error.
func TestListWithUnreachableVaultStillFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	var stdout, stderr bytes.Buffer
	ctx := &Ctx{
		G:   Global{Vault: missing, JSON: true},
		Cfg: &config.Config{Backup: "none", Verify: "size"},
		UI:  &ui.UI{Out: &stdout, Err: &stderr, JSON: true},
	}
	err := ctx.cmdList([]string{"--json"})
	if err == nil {
		t.Fatal("list succeeded with an unreachable vault, want exit 5")
	}
	if code := exitCode(err); code != ExitVaultUnreach {
		t.Fatalf("exit code = %d, want %d (vault unreachable): %v", code, ExitVaultUnreach, err)
	}
}
