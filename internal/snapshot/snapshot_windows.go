//go:build windows

package snapshot

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func createSnapshot() (string, int, error) {
	const create = `Checkpoint-Computer -Description "Midas pre-move" -RestorePointType "MODIFY_SETTINGS"`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", create).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", 0, fmt.Errorf("%s (System Restore needs an elevated shell and System Protection enabled for the system drive; Windows also allows about one restore point per 24 hours)", msg)
	}
	seq := 0
	const query = `(Get-ComputerRestorePoint | Sort-Object SequenceNumber | Select-Object -Last 1).SequenceNumber`
	if q, qerr := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query).Output(); qerr == nil {
		seq, _ = strconv.Atoi(strings.TrimSpace(string(q)))
	}
	return "Midas pre-move", seq, nil
}
