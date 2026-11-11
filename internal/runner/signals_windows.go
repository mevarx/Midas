//go:build windows

package runner

import (
	"os"
	"os/exec"
	"os/signal"
)

// forwardSignals keeps Midas alive while the child handles Ctrl+C in the
// shared console, then lets Wait return the child's exit code. Windows does
// not support sending os.Interrupt to another process.
func forwardSignals(cmd *exec.Cmd) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				// Deliberately ignored: the child receives Ctrl+C directly.
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
