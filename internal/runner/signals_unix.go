//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// forwardSignals relays SIGINT/SIGTERM to the child and returns a stop func.
func forwardSignals(cmd *exec.Cmd) func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-ch:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(sig)
				}
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
