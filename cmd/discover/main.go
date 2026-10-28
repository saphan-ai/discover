// Command discover reads a repository and reports what it finds.
package main

import (
	"os"

	"github.com/saphan-ai/discover/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
