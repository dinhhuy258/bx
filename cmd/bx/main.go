// Command bx is the entry point. All logic is in internal/.
package main

import (
	"os"

	"github.com/dinhhuy258/bx/internal/cli"
	_ "github.com/dinhhuy258/bx/internal/commands" // registers the real commands (added in later tasks)
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
