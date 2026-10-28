// Package cli is the command line of discover.
package cli

import (
	"fmt"
	"io"
)

// version and commit are set at build time with -ldflags -X.
var (
	version = "dev"
	commit  = "unknown"
)

const usage = `usage: discover <command>

commands:
  scan      scan a repository (not built yet)
  verify    verify a report (not built yet)
  version   print the version
  help      print this list
`

// Run executes one invocation and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return ExitOK
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ExitOK
	case "version":
		fmt.Fprintf(stdout, "discover %s (%s)\n", version, commit)
		return ExitOK
	case "scan", "verify":
		fmt.Fprintf(stderr, "discover %s: not built yet\n", args[0])
		return ExitUsage
	default:
		fmt.Fprintf(stderr, "discover: unknown command %q\n\n%s", args[0], usage)
		return ExitUsage
	}
}
