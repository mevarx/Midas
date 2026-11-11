package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed completions/*
var completionFiles embed.FS

func (c *Ctx) cmdCompletion(args []string) error {
	if len(args) != 1 {
		return failf(ExitUsage, "usage: midas completion <bash|zsh|fish|powershell>")
	}
	shell := strings.ToLower(args[0])
	if shell != "bash" && shell != "zsh" && shell != "fish" && shell != "powershell" {
		return failf(ExitUsage, "unsupported shell %q (choose bash, zsh, fish or powershell)", args[0])
	}
	script, err := fs.ReadFile(completionFiles, "completions/"+shell)
	if err != nil {
		return fmt.Errorf("read %s completion: %w", shell, err)
	}
	_, err = c.UI.Out.Write(script)
	return err
}
