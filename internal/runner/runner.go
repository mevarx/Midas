// Package runner launches an app with its temp directories redirected into
// the vault and forwards signals and the exit code.
package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/midas-cli/midas/internal/platform"
)

// Options controls one run.
type Options struct {
	Vault         string
	VaultApp      string
	Env           map[string]string
	AllowFallback bool
	CleanTmp      bool
	Command       []string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
}

// EnvError distinguishes "vault unreachable" from other failures so the CLI
// can return exit code 5.
type EnvError struct{ Err error }

func (e *EnvError) Error() string { return e.Err.Error() }
func (e *EnvError) Unwrap() error { return e.Err }

// Run starts the child and waits for it. The returned int is the child's
// exit code.
func Run(o Options) (int, error) {
	if len(o.Command) == 0 {
		return 0, errors.New("no command given")
	}
	tmp := filepath.Join(o.VaultApp, "tmp")
	useTmp := true
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		if !o.AllowFallback {
			return 0, &EnvError{Err: fmt.Errorf("vault is unreachable (cannot create %s): %w", tmp, err)}
		}
		useTmp = false
	} else if err := checkWritable(tmp); err != nil {
		if !o.AllowFallback {
			return 0, &EnvError{Err: fmt.Errorf("vault temp directory is not writable: %w", err)}
		}
		useTmp = false
	}

	env := os.Environ()
	if useTmp {
		for _, name := range platform.Current().TempEnvVars() {
			env = setEnv(env, name, tmp)
		}
	}
	for k, v := range o.Env {
		env = setEnv(env, k, v)
	}

	cmd := exec.Command(o.Command[0], o.Command[1:]...)
	cmd.Stdin = o.Stdin
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %q: %w", o.Command[0], err)
	}
	stop := forwardSignals(cmd)
	err := cmd.Wait()
	stop()

	if o.CleanTmp && useTmp {
		if cerr := cleanDir(tmp); cerr != nil {
			fmt.Fprintf(o.Stderr, "warning: could not fully clean %s: %v\n", tmp, cerr)
		}
	}
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code < 0 {
			code = 1
		}
		return code, nil
	}
	return 1, err
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".midas-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	out := env[:0]
	for _, e := range env {
		name := e
		if i := strings.IndexByte(e, '='); i >= 0 {
			name = e[:i]
		}
		if platform.EnvKeysEqual(name, key) {
			continue
		}
		out = append(out, e)
	}
	return append(out, prefix+val)
}

func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
