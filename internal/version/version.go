// Package version holds build identity used in output and manifests.
package version

// Version is overridden by GoReleaser via -ldflags at release time.
var Version = "1.1.0"

// Commit is injected by GoReleaser for traceable release artifacts.
var Commit = "unknown"
