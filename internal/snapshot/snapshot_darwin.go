//go:build darwin

package snapshot

import (
	"fmt"
	"os/exec"
	"strings"
)

func createSnapshot() (string, int, error) {
	out, err := exec.Command("tmutil", "localsnapshot").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", 0, fmt.Errorf("tmutil localsnapshot: %s", msg)
	}
	name := "APFS local snapshot"
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		name = strings.TrimSpace(lines[len(lines)-1])
	}
	return name, 0, nil
}
