//go:build windows

package platform

import "fmt"

// ShimFileName returns the wrapper name for a command on this OS.
func ShimFileName(base string) string { return base + ".cmd" }

// ShimScript renders a wrapper that runs the real executable through
// `midas run`, forwarding all arguments.
func ShimScript(midasExe, app, real string) string {
	return fmt.Sprintf("@echo off\r\n\"%s\" run %s -- \"%s\" %%*\r\n", midasExe, app, real)
}
