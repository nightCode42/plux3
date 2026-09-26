// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/nightCode42/plux3/backend/internal/buildinfo"
)

// name is the binary name used in output and usage text.
const name = "plux"

// usage lists the commands this binary accepts.
const usage = `Usage: plux <command>

Commands:
  version   Print version information
  help      Show this help
`

// Exit codes follow the common convention: 0 success, 2 usage error.
const (
	exitOK    = 0
	exitUsage = 2
)

// main delegates to run so that the command logic is testable without
// terminating the test process.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command given by args and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "version", "--version":
		_, _ = fmt.Fprintln(stdout, name, buildinfo.Get())
		return exitOK
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "%s: unknown command %q\n\n%s", name, args[0], usage)
		return exitUsage
	}
}
