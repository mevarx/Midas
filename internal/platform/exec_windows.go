//go:build windows

package platform

import (
	"os/exec"
)

// runCmd runs a helper command and returns its combined output. It is used
// only for fallbacks (mklink) and must never be exposed to app definitions.
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
