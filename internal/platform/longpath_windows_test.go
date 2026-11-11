//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// longPathFixture builds a directory chain whose full path exceeds the
// traditional 260-character MAX_PATH boundary while every component stays
// within NTFS limits.
func longPathFixture(t *testing.T, base, name string) string {
	t.Helper()
	path := filepath.Join(base, name)
	segment := "level-"
	for len(path) <= 260 {
		next := filepath.Join(path, segment+strings.Repeat("d", 24))
		if len(next) > 255*4 { // component guard, unreachable in practice
			t.Fatalf("fixture construction failed at %q", path)
		}
		if err := os.MkdirAll(next, 0o700); err != nil {
			t.Fatal(err)
		}
		path = next
	}
	if len(path) <= 260 {
		t.Fatal("fixture did not exceed the 260-character boundary")
	}
	return path
}

func TestCreateLinkBeyondMaxPath(t *testing.T) {
	base := t.TempDir()
	target := longPathFixture(t, base, "target")
	// The junction itself must not pre-exist: build only its ancestors.
	linkParent := longPathFixture(t, base, "link")
	link := filepath.Join(linkParent, "junction")
	if len(target) <= 260 || len(link) <= 260 {
		t.Fatalf("fixture paths must exceed 260 characters: target=%d link=%d", len(target), len(link))
	}
	data := filepath.Join(target, "payload.bin")
	if err := os.WriteFile(data, []byte("longpath"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Current().CreateLink(link, target); err != nil {
		t.Fatalf("CreateLink beyond MAX_PATH: %v", err)
	}
	t.Cleanup(func() { _ = Current().RemoveLink(link) })
	got, isLink, err := Current().ReadLink(link)
	if err != nil || !isLink {
		t.Fatalf("ReadLink beyond MAX_PATH = %q, %t, %v", got, isLink, err)
	}
	if !SamePath(got, target) {
		t.Fatalf("link target = %q, want %q", got, target)
	}
	resolved, err := Current().ResolvePath(link)
	if err != nil || !SamePath(resolved, target) {
		t.Fatalf("ResolvePath beyond MAX_PATH = %q, %v", resolved, err)
	}
	content, err := os.ReadFile(filepath.Join(link, "payload.bin"))
	if err != nil || string(content) != "longpath" {
		t.Fatalf("data unreachable through long link: %q, %v", content, err)
	}
	volume, err := Current().VolumeID(link)
	if err != nil || volume == "" {
		t.Fatalf("VolumeID beyond MAX_PATH = %q, %v", volume, err)
	}
	if err := Current().RemoveLink(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(data); err != nil {
		t.Fatalf("junction removal disturbed its target: %v", err)
	}
}
