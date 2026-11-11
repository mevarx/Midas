package main

import (
	"os"

	"github.com/midas-cli/midas/internal/cli"
)

func main() {
	ctx, stop := cli.InterruptContext()
	code := cli.RunContext(os.Args[1:], ctx)
	stop()
	os.Exit(code)
}
