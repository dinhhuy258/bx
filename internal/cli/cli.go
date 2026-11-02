// Package cli dispatches bx subcommands. Each command is registered with a
// name, a one-line help and a function; the real work lives in other packages.
package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Version is set at build time with -ldflags "-X github.com/dinhhuy258/bx/internal/cli.Version=...".
var Version = "dev"

// Command is one subcommand. Args excludes the command name.
type Command struct {
	Name string
	Help string
	Run  func(args []string, out, errOut io.Writer) int
}

var commands = map[string]Command{}

// Register adds a command. Later tasks call it from init() in their packages.
func Register(c Command) { commands[c.Name] = c }

// Run executes args[0] and returns the exit code. No args, or a first arg that is a client
// flag (--server, --project), runs "client".
func Run(args []string, out, errOut io.Writer) int {
	name := "client"
	if len(args) > 0 {
		switch a := args[0]; {
		case a == "--version" || a == "-v" || a == "--help" || a == "-h":
			name, args = a, args[1:]
		case !strings.HasPrefix(a, "-"):
			name, args = a, args[1:]
		}
	}
	switch name {
	case "version", "--version", "-v":
		fmt.Fprintf(out, "bx %s\n", Version)
		return 0
	case "help", "--help", "-h":
		printHelp(out)
		return 0
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(errOut, "bx: unknown command %q\n", name)
		return 2
	}
	return c.Run(args, out, errOut)
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "bx: Claude Code sessions on a server, driven from any terminal")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage: bx [--server URL] [--project NAME]   (the client)")
	fmt.Fprintln(out, "       bx <command> [args]")
	fmt.Fprintln(out)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(out, "  %-10s %s\n", n, commands[n].Help)
	}
}

func init() {
	// Placeholders so `bx help` is complete from day one. Later tasks replace them.
	for _, c := range []Command{
		{Name: "client", Help: "[--server URL] [--project NAME]: the bx client (default)"},
		{Name: "serve", Help: "the daemon: projects, stories, sessions and their streams (run by systemd)"},
		{Name: "notify", Help: "internal: Claude Code hook callback, posts to ntfy"},
		{Name: "hooks", Help: "hooks install: add the bx notify hooks to ~/.claude/settings.json"},
		{Name: "claude", Help: "claude sync [folders] [--dry-run] [--yes]: copy Claude skills, agents, commands and rules to bx serve"},
	} {
		if _, exists := commands[c.Name]; !exists {
			c.Run = notImplemented(c.Name)
			Register(c)
		}
	}
}

func notImplemented(name string) func([]string, io.Writer, io.Writer) int {
	return func(_ []string, _, errOut io.Writer) int {
		fmt.Fprintf(errOut, "bx %s: not implemented yet\n", name)
		return 1
	}
}
