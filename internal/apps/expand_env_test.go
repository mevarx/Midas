package apps

import (
	"path/filepath"
	"testing"
)

// TestExpandPathEnvDefaults covers ${VAR:-default} semantics: the default
// applies when VAR is unset or empty, and an explicit value always wins.
func TestExpandPathEnvDefaults(t *testing.T) {
	home, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"home": home}

	cases := []struct {
		name string
		raw  string
		env  map[string]string
		want string
	}{
		{
			name: "unset uses default",
			raw:  "${MIDAS_TEST_HOME:-~/.codex}",
			env:  map[string]string{"MIDAS_TEST_HOME": ""},
			want: filepath.Join(home, ".codex"),
		},
		{
			name: "empty uses default",
			raw:  "${MIDAS_TEST_HOME:-~/.codex}",
			env:  map[string]string{"MIDAS_TEST_HOME": ""},
			want: filepath.Join(home, ".codex"),
		},
		{
			name: "set wins over default",
			raw:  "${MIDAS_TEST_HOME:-~/.codex}",
			env:  map[string]string{"MIDAS_TEST_HOME": filepath.Join(home, "elsewhere")},
			want: filepath.Join(home, "elsewhere"),
		},
		{
			name: "default with token",
			raw:  "${MIDAS_TEST_HOME:-{home}/.gemini}",
			env:  map[string]string{"MIDAS_TEST_HOME": ""},
			want: filepath.Join(home, ".gemini"),
		},
		{
			name: "default joined with suffix",
			raw:  "${MIDAS_TEST_HOME:-~/.amp}/threads",
			env:  map[string]string{"MIDAS_TEST_HOME": ""},
			want: filepath.Join(home, ".amp", "threads"),
		},
		{
			name: "two defaults in one path",
			raw:  "${MIDAS_TEST_A:-~/.x}/${MIDAS_TEST_B:-sub}",
			env:  map[string]string{"MIDAS_TEST_A": "", "MIDAS_TEST_B": ""},
			want: filepath.Join(home, ".x", "sub"),
		},
		{
			name: "braced var without default still resolves",
			raw:  "${MIDAS_TEST_HOME}/data",
			env:  map[string]string{"MIDAS_TEST_HOME": filepath.Join(home, "braced")},
			want: filepath.Join(home, "braced", "data"),
		},
		{
			name: "bare var still resolves",
			raw:  "$MIDAS_TEST_HOME/data",
			env:  map[string]string{"MIDAS_TEST_HOME": filepath.Join(home, "bare")},
			want: filepath.Join(home, "bare", "data"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got, err := expandWithTokens(tc.raw, tokens)
			if err != nil {
				t.Fatalf("expand %q: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("expand %q = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestExpandPathUnresolved verifies that placeholders without values are
// still rejected after the env-default extension.
func TestExpandPathUnresolved(t *testing.T) {
	t.Setenv("MIDAS_TEST_UNSET", "")
	tokens := map[string]string{"home": t.TempDir()}
	for _, raw := range []string{
		"${MIDAS_TEST_UNSET}/data", // braced, no default, unset
		"$MIDAS_TEST_UNSET/data",   // bare, unset
		"{no_such_token}/data",     // unknown token
	} {
		if _, err := expandWithTokens(raw, tokens); err == nil {
			t.Errorf("expand %q succeeded, want unresolved-placeholder error", raw)
		}
	}
}

// TestExpandPathEmptyDefaultRejected verifies ${VAR:-} yields an empty
// segment and is rejected rather than silently producing a partial path.
func TestExpandPathEmptyDefaultRejected(t *testing.T) {
	t.Setenv("MIDAS_TEST_EMPTY", "")
	tokens := map[string]string{"home": t.TempDir()}
	if _, err := expandWithTokens("${MIDAS_TEST_EMPTY:-}", tokens); err == nil {
		t.Fatal("empty default expanded without error")
	}
}
