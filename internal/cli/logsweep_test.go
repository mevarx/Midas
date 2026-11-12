package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mevarx/Midas/internal/config"
	"github.com/mevarx/Midas/internal/ui"
)

func TestLogfSweepsLogsOlderThanRetention(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, ".midas", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldLog := filepath.Join(dir, "old.log")
	recentLog := filepath.Join(dir, "recent.log")
	for _, name := range []string{oldLog, recentLog} {
		if err := os.WriteFile(name, []byte("entry\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(oldLog, old, old); err != nil {
		t.Fatal(err)
	}

	ctx := &Ctx{G: Global{Vault: vault}, Cfg: &config.Config{}, UI: &ui.UI{}}
	defer ctx.closeLog()
	ctx.Logf("hello")

	if _, err := os.Stat(oldLog); !os.IsNotExist(err) {
		t.Fatalf("old log should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(recentLog); err != nil {
		t.Fatalf("recent log should remain: %v", err)
	}
	today := filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
	if _, err := os.Stat(today); err != nil {
		t.Fatalf("today's log should exist: %v", err)
	}
}
