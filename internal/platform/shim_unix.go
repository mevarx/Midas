//go:build !windows

package platform

import "fmt"

// ShimFileName returns the wrapper name for a command on this OS.
func ShimFileName(base string) string { return base }

// ShimScript renders a POSIX shell wrapper that runs the real executable
// through `midas run`, forwarding all arguments.
func ShimScript(midasExe, app, real string) string {
	return fmt.Sprintf("#!/bin/sh\nexec \"%s\" run %s -- \"%s\" \"$@\"\n", midasExe, app, real)
}
