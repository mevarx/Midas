//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunnerForwardsTerminationSignal(t *testing.T) {
	if os.Getenv("MIDAS_SIGNAL_CHILD") == "1" {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		if err := os.WriteFile(os.Getenv("MIDAS_SIGNAL_READY"), []byte("ready"), 0o600); err != nil {
			os.Exit(3)
		}
		<-ch
		os.Exit(0)
	}
	if os.Getenv("MIDAS_SIGNAL_OUTER") == "1" {
		exe, err := os.Executable()
		if err != nil {
			os.Exit(4)
		}
		code, err := Run(Options{
			VaultApp: t.TempDir(),
			Env: map[string]string{
				"MIDAS_SIGNAL_CHILD": "1", "MIDAS_SIGNAL_READY": os.Getenv("MIDAS_SIGNAL_READY"),
			},
			Command: []string{exe, "-test.run=^TestRunnerForwardsTerminationSignal$"},
			Stdin:   os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		})
		if err != nil {
			os.Exit(5)
		}
		os.Exit(code)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(exe, "-test.run=^TestRunnerForwardsTerminationSignal$")
	cmd.Env = append(os.Environ(), "MIDAS_SIGNAL_OUTER=1", "MIDAS_SIGNAL_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("signal child did not become ready: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runner process exited unsuccessfully after forwarded signal: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("runner did not return after forwarding SIGTERM")
	}
}
