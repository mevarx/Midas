package main

import (
	"os"

	"github.com/midas-cli/midas/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
