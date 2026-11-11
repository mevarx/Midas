package apps

import (
	"path/filepath"
	"testing"

	"github.com/midas-cli/midas/internal/platform"
)

func TestValidateTargetPathRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(home, "linked")
	if err := platform.Current().CreateLink(link, external); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	target := filepath.Join(link, "state")
	if err := ValidateTargetPath(target, home, filepath.Join(home, "vault")); err == nil {
		t.Fatal("path through symlink outside home was accepted")
	}
}
