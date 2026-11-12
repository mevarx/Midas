//go:build !windows

package mover

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHealthCheckRejectsUnreadableFileThroughLink(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 000 file")
	}
	original, dest := linkHealthCheckDest(t)
	file := filepath.Join(dest, "secret.txt")
	if err := os.WriteFile(file, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o600) })

	healthy, detail := HealthCheck(original, dest)
	if healthy {
		t.Fatal("HealthCheck reported an unreadable file through the link as healthy")
	}
	if detail == "" {
		t.Fatal("HealthCheck failed without a detail")
	}
}
