// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
)

// commands are the top-level commands, for help and completion.
var commands = []string{
	"login", "logout", "whoami", "init", "doctor", "validate", "build", "diff", "publish", "pull",
	"release", "export", "import", "keys", "completion", "version", "help",
}

// releaseCommands are the release subcommands.
var releaseCommands = []string{"list", "promote", "rollback"}

// completion prints a completion script for a shell (CLI-008).
func (e env) completion(args []string) int {
	if len(args) != 1 {
		_, _ = fmt.Fprint(e.stderr, "Usage: plux completion bash|zsh|fish|powershell\n")
		return exitUsage
	}
	all, rel := strings.Join(commands, " "), strings.Join(releaseCommands, " ")
	var script string
	switch args[0] {
	case "bash":
		script = `_plux() {
  local cur=${COMP_WORDS[COMP_CWORD]}
  if [ "$COMP_CWORD" -eq 1 ]; then COMPREPLY=($(compgen -W "` + all + `" -- "$cur"))
  elif [ "${COMP_WORDS[1]}" = release ] && [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=($(compgen -W "` + rel + `" -- "$cur"))
  else COMPREPLY=($(compgen -f -- "$cur")); fi
}
complete -F _plux plux
`
	case "zsh":
		script = `#compdef plux
_plux() {
  if (( CURRENT == 2 )); then compadd ` + all + `
  elif [[ ${words[2]} == release ]] && (( CURRENT == 3 )); then compadd ` + rel + `
  else _files; fi
}
compdef _plux plux
`
	case "fish":
		script = "complete -c plux -f -n '__fish_use_subcommand' -a '" + all + "'\n" +
			"complete -c plux -f -n '__fish_seen_subcommand_from release' -a '" + rel + "'\n"
	case "powershell":
		script = `Register-ArgumentCompleter -Native -CommandName plux -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = $commandAst.CommandElements
  $list = if ($words.Count -ge 2 -and $words[1].ToString() -eq 'release') { '` + rel + `' } else { '` + all + `' }
  $list.Split(' ') | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`
	default:
		_, _ = fmt.Fprintf(e.stderr, "%s completion: unknown shell %q\n", name, args[0])
		return exitUsage
	}
	_, _ = fmt.Fprint(e.stdout, script)
	return exitOK
}
