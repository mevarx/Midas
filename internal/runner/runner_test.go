package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mevarx/Midas/internal/platform"
)

func TestRunRedirectsTempEnvironment(t *testing.T) {
	if os.Getenv("MIDAS_TEST_TEMP_CHILD") == "1" {
		want := os.Getenv("MIDAS_TEST_EXPECT_TEMP")
		for _, key := range platform.Current().TempEnvVars() {
			if got := os.Getenv(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		return
	}
	vaultApp := t.TempDir()
	want := filepath.Join(vaultApp, "tmp")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(Options{
		VaultApp: vaultApp,
		Env:      map[string]string{"MIDAS_TEST_TEMP_CHILD": "1", "MIDAS_TEST_EXPECT_TEMP": want},
		Command:  []string{exe, "-test.run=^TestRunRedirectsTempEnvironment$"},
		Stdout:   &stdout, Stderr: &stderr,
	})
	if err != nil || code != 0 {
		t.Fatalf("Run = %d, %v; stderr: %s", code, err, stderr.String())
	}
}

func TestRunPreservesChildExitCode(t *testing.T) {
	if value := os.Getenv("MIDAS_TEST_EXIT_CHILD"); value != "" {
		code, _ := strconv.Atoi(value)
		os.Exit(code)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Run(Options{
		VaultApp: t.TempDir(),
		Env:      map[string]string{"MIDAS_TEST_EXIT_CHILD": "23"},
		Command:  []string{exe, "-test.run=^TestRunPreservesChildExitCode$"},
		Stdin:    &bytes.Buffer{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if err != nil || code != 23 {
		t.Fatalf("Run = %d, %v; want child exit 23", code, err)
	}
}
