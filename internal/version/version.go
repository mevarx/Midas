// Package version holds build identity used in output and manifests.
//
// No release number lives in the repository: release builds receive
// Version and Commit from GoReleaser via -ldflags (derived from the git
// tag), and source builds fall back to the embedded build info.
package version

import (
	"runtime/debug"
	"strings"
)

var (
	// Version is injected at link time for release builds.
	Version string
	// Commit is injected at link time for release builds.
	Commit string
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		if Version == "" {
			Version = "dev"
		}
		if Commit == "" {
			Commit = "unknown"
		}
		return
	}
	if Version == "" {
		// `go install <module>@<tag>` records the module version; local
		// builds report "(devel)". Strip the leading v so source installs
		// report the same form as release binaries.
		if v := info.Main.Version; v != "" && v != "(devel)" {
			Version = strings.TrimPrefix(v, "v")
		} else {
			Version = "dev"
		}
	}
	if Commit == "" {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				Commit = s.Value
				break
			}
		}
		if Commit == "" {
			Commit = "unknown"
		}
	}
}
