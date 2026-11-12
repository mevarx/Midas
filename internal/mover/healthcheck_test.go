package mover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mevarx/Midas/internal/platform"
)

// linkHealthCheckDest builds an empty vault directory and a Midas link to it.
func linkHealthCheckDest(t *testing.T) (original, dest string) {
	t.Helper()
	root := t.TempDir()
	dest = filepath.Join(root, "vault")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	original = filepath.Join(root, "original")
	if err := platform.Current().CreateLink(original, dest); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	t.Cleanup(func() { _ = platform.Current().RemoveLink(original) })
	return original, dest
}

func TestHealthCheckReadsFileInSubdirectory(t *testing.T) {
	original, dest := linkHealthCheckDest(t)
	sub := filepath.Join(dest, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "inside.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	healthy, detail := HealthCheck(original, dest)
	if !healthy {
		t.Fatalf("HealthCheck = false, %q; want healthy for a file in a subdirectory", detail)
	}
}

func TestHealthCheckEmptyTreeIsHealthy(t *testing.T) {
	original, dest := linkHealthCheckDest(t)

	healthy, detail := HealthCheck(original, dest)
	if !healthy {
		t.Fatalf("HealthCheck = false, %q; want healthy for an empty tree", detail)
	}
	if !strings.Contains(detail, "no regular file") {
		t.Fatalf("detail = %q; want it to explain that no file exists to read", detail)
	}
}
