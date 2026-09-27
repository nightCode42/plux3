# CLI Reference

`plux` is the command-line interface for developers and CI (spec §22.1). It is a single Go binary, `backend/cmd/plux`; later phases add the server commands of `CLI-003`. This page documents the commands that exist.

## Commands

| Command | Does |
|---|---|
| `plux validate [--json] <project-dir>` | Compiles a project in the [Git layout](document-model.md#1-project-layout) and reports every diagnostic, without writing anything. |
| `plux build [--dev] [--json] -o <out-dir> <project-dir>` | Compiles the project and writes its bundles (below). |
| `plux version` | Prints the version, commit, commit date, Go version and platform of the binary. |
| `plux help` | Lists the commands. `plux <command> -h` lists a command's flags. |

`validate` and `build` work fully offline against a local directory, so CI can check a change without a server (`CLI-005`). They run the compiler described in [compiler.md](compiler.md) with the registry's default limits and record the binary's version in every bundle. Flags come before the project directory.

`build` writes the app bundle to `<out-dir>/app/<key>.pxb` and one bundle per plugin to `<out-dir>/plugins/<key>.pxb`. A release build also writes each bundle's source map beside it as `<key>.sourcemap` (`CMP-041`); `--dev` builds development bundles, which embed their source maps instead. Files are written through a temporary file and a rename, so a reader never sees a partial bundle. Nothing is written when the project has errors.

## Output and exit codes

Diagnostics go to standard error, one per line as `file#pointer[start:end]: severity PLX-nnnn message`, followed by a count; `build` prints each written bundle's hash (`BND-005`), path and size on standard output.

With `--json`, standard output carries one JSON object and nothing else:

| Field | Content |
|---|---|
| `ok` | `true` when no error was reported |
| `errors`, `warnings` | Counts |
| `bundles` | `build` only: `role` (`app` or `plugin`), `key`, `id`, `development`, `file`, `sourceMap` (release builds), `size`, `hash` (hex), `features` (required features, sorted) |
| `diagnostics` | Every diagnostic in the form of [ADR-0018](../adr/0018-unified-error-model.md): `code`, `reason`, `severity`, `file`, `path`, `range`, `message`, `cause`, `fix`, `docURL` |

| Exit code | Meaning |
|---|---|
| 0 | Success; warnings do not fail a command |
| 1 | The command ran and failed: the project has errors, or the output could not be written |
| 2 | Usage error: an unknown command or flag, a missing argument, or a project path that is not a directory |
