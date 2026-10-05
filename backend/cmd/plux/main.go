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
const usage = `Usage: plux <command> [flags]

Commands:
  login       Sign in to a Plux Server (device authorization grant)
  logout      Forget the stored token
  whoami      Show the signed-in user
  init        Record the project's server, organisation and app in plux.json
  doctor      Check the project, server, credential and app
  validate    Validate a project directory offline
  build       Compile a project directory into bundles offline
  codegen     Write a project's typed Dart API into the host app
  test        Run the project's test scenarios headlessly with Flutter
  diff        Compare the project with the server's drafts
  publish     Upload, publish and optionally release and promote
  pull        Download a channel's release and keys as the host's baseline
  release     list | promote | rollback releases
  export      Write the server's drafts into the project
  import      Replace the server's drafts with the project
  keys        List an environment's public keys
  native      scan | sync the host app's native catalogue
  create      Generate the Flutter project of a no-code app
  completion  Print a shell completion script (bash, zsh, fish, powershell)
  version     Print version information
  help        Show this help

Run 'plux <command> -h' for the flags of a command.
`

// Exit codes: 0 success, 1 the command ran and failed (a project with
// errors, output that cannot be written, a failed publish), 2 usage
// error, 3 not signed in or refused, 4 server unreachable (CLI-007).
const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
)

// main delegates to run so that the command logic is testable without
// terminating the test process.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command given by args and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "validate":
			return validate(args[1:], stdout, stderr)
		case "build":
			return build(args[1:], stdout, stderr)
		case "codegen":
			return codegenCmd(args[1:], stdout, stderr)
		case "test":
			return testCmd(args[1:], stdout, stderr)
		}
		e := newEnv(stdout, stderr)
		if cmd, ok := e.commands()[args[0]]; ok {
			return cmd(args[1:])
		}
	}
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

// commands maps the server commands to their implementations.
func (e env) commands() map[string]func([]string) int {
	return map[string]func([]string) int{
		"login": e.login, "logout": e.logout, "whoami": e.whoami, "init": e.initProject, "doctor": e.doctor,
		"diff": e.diff, "publish": e.publish, "pull": e.pull, "release": e.release, "export": e.export,
		"import": e.importCmd, "keys": e.keys, "native": e.native, "create": e.create, "completion": e.completion,
	}
}
