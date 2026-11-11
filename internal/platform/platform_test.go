package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryLinkRemovalPreservesDestination(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "destination")
	link := filepath.Join(root, "original")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(target, "data")
	if err := os.WriteFile(data, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Current().CreateLink(link, target); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	got, isLink, err := Current().ReadLink(link)
	if err != nil || !isLink || !SamePath(got, target) {
		t.Fatalf("ReadLink = %q, %v, %v", got, isLink, err)
	}
	if err := Current().RemoveLink(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link remains after removal: %v", err)
	}
	if got, err := os.ReadFile(data); err != nil || string(got) != "preserve" {
		t.Fatalf("removing link changed destination: %q, %v", got, err)
	}
}

func TestCreateLinkRejectsExistingDirectoryWithoutChangingIt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "existing")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(link, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(link, "user")
	if err := os.WriteFile(data, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Current().CreateLink(link, target); err == nil {
		t.Fatal("link creation overwrote an existing directory")
	}
	if got, err := os.ReadFile(data); err != nil || string(got) != "keep" {
		t.Fatalf("existing directory changed: %q, %v", got, err)
	}
}
