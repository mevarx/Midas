package version

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIdentityIsDerived proves the build identity is populated without a
// hardcoded release number: source builds report a development marker and
// the embedded VCS commit when available; release builds receive both from
// -ldflags.
func TestIdentityIsDerived(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty; build identity must be derived")
	}
	if Commit == "" {
		t.Fatal("Commit is empty; build identity must be derived")
	}
}

// TestNoHardcodedVersion pins the release-identity contract: no release
// number may live in the package source. Versions come from the git tag via
// GoReleaser ldflags or from embedded build info, never from a literal.
// Every non-test source file in the package is scanned, so a literal
// reintroduced in a new file cannot escape the gate.
func TestNoHardcodedVersion(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob package sources: %v (%d files)", err, len(files))
	}
	re := regexp.MustCompile(`\d+\.\d+\.\d+`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if re.Match(src) {
			t.Fatalf("%s contains a hardcoded version number:\n%s", f, src)
		}
	}
}
