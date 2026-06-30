// Command idle-hands is a pocket-sized break coach for the dead time while your
// AI coding agent thinks. It wires up subcommand routing, a `version` command,
// the `watch` wrapper that runs your agent transparently, detects its
// BUSY/IDLE windows, and shows one micro-action card while it's thinking, and a
// `stats` command that reports the idle time you've reclaimed. The deck, busy
// threshold, and quiet hours are read from ~/.idle-hands/config.toml.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rwrife/idle-hands/internal/version"
)

// stdout is the writer for command output (as opposed to notices, which go to
// os.Stderr). It is a package var so tests can capture `stats` output without a
// real terminal.
var stdout io.Writer = os.Stdout

const usage = `idle-hands 🙌 — one good micro-win for the dead time while your agent thinks.

Usage:
  idle-hands <command> [args...]

Commands:
  watch -- <cmd> [args...]   Run <cmd>, passing I/O straight through, and show
                             one micro-action card while it's "thinking".
  stats                      Show reclaimed idle time ("reclaimed X min today").
  version                    Print the build version.
  help                       Show this help.

Config (optional): ~/.idle-hands/config.toml
  deck = "move"            # move | duck | tidy
  busy_threshold = "20s"  # how long quiet before a card fires
  [quiet_hours]           # suppress cards during these local hours
  start = "22:00"
  end   = "07:00"

Examples:
  idle-hands watch -- echo hi
  idle-hands watch -- claude
  idle-hands stats
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
		code, err := cmdStats(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, "idle-hands: "+err.Error())
		}
		return code

	default:
		fmt.Fprintf(os.Stderr, "idle-hands: unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// errNoCommand is returned when `watch` is invoked without a command to run.
var errNoCommand = errors.New("watch: no command given (usage: idle-hands watch -- <cmd> [args...])")
