//go:build linux

package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func createSnapshot() (string, int, error) {
	ts, err := exec.LookPath("timeshift")
	if err != nil {
		if isBtrfsRoot() {
			return "", 0, fmt.Errorf("no automatic snapshot tool; the root filesystem is btrfs, so you could run `btrfs subvolume snapshot / /snapshots/pre-midas` by hand. The verified file backup below is the safety net")
		}
		return "", 0, fmt.Errorf("Timeshift is not installed; install it or accept the file backup as the safety net")
	}
	if os.Geteuid() != 0 {
		return "", 0, fmt.Errorf("Timeshift snapshots need root; run Midas with sudo or accept the file backup as the safety net")
	}
	out, err := exec.Command(ts, "--create", "--comments", "Midas pre-move", "--scripted").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", 0, fmt.Errorf("timeshift --create: %s", msg)
	}
	return "Timeshift pre-move snapshot", 0, nil
}

func isBtrfsRoot() bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == "/" && fields[2] == "btrfs" {
			return true
		}
	}
	return false
}
