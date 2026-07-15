package main

import (
	"os"

	"github.com/plystra/cli/internal/command"
)

func main() {
	os.Exit(command.Run(os.Args[1:], os.Stdout, os.Stderr))
}
