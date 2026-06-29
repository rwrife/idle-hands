// Command idle-hands is a pocket-sized break coach for the dead time while your
// AI coding agent thinks. It wires up subcommand routing, a `version` command,
// and the `watch` wrapper that runs your agent transparently, detects its
// BUSY/IDLE windows, and shows one micro-action card while it's thinking.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/rwrife/idle-hands/internal/version"
)

const usage = `idle-hands 🙌 — one good micro-win for the dead time while your agent thinks.

Usage:
  idle-hands <command> [args...]

Commands:
  watch -- <cmd> [args...]   Run <cmd>, passing I/O straight through, and show
                             one micro-action card while it's "thinking".
  stats                      Show reclaimed idle time. (coming in M5)
  version                    Print the build version.
  help                       Show this help.

Examples:
  idle-hands watch -- echo hi
  idle-hands watch -- claude
  idle-hands version
`

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches a subcommand and returns the process exit code. It is kept
// separate from main so it can be unit-tested without calling os.Exit.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("idle-hands", version.Detail())
		return 0

	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0

	case "watch":
		code, err := cmdWatch(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, "idle-hands: "+err.Error())
		}
		return code

	case "stats":
		fmt.Fprintln(os.Stderr, "idle-hands: `stats` is not implemented yet (coming in M5).")
		return 1

	default:
		fmt.Fprintf(os.Stderr, "idle-hands: unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// errNoCommand is returned when `watch` is invoked without a command to run.
var errNoCommand = errors.New("watch: no command given (usage: idle-hands watch -- <cmd> [args...])")
