# go-themida

[![Go Reference](https://pkg.go.dev/badge/github.com/Rubberbandits/go-themida.svg)](https://pkg.go.dev/github.com/Rubberbandits/go-themida)
[![CI](https://github.com/Rubberbandits/go-themida/actions/workflows/ci.yml/badge.svg)](https://github.com/Rubberbandits/go-themida/actions/workflows/ci.yml)

A Go package for reading, editing, writing, and applying [Themida](https://www.oreans.com/Themida.php)
protection projects.

Themida (by Oreans Technologies) is a Windows software protector. A protection
project is a `.tmd` file: an INI-like document under a single `[SECURE_ENGINE]`
section whose `KEY=VALUE` entries configure how an input executable is protected
and where the protected output is written. This package models those settings in
Go and drives the protector itself, natively on Windows or — through a pluggable
execution environment such as [go-wine](https://github.com/Rubberbandits/go-wine)
— on other hosts.

## Features

- **Lossless editing.** Parsing and re-serializing a project preserves every
  unmodeled setting, every comment, and the original key order, so edits produce
  minimal diffs.
- **Typed settings.** Common groups are exposed as Go structs — protection
  macros (string encryption, function mapping, integrity checks), customized
  dialogs, and XBundler file bundling — while any other setting remains reachable
  by its native key.
- **Atomic overrides.** A batch of changes is applied all-or-nothing; invalid
  input leaves the project untouched.
- **Execution.** A reusable `Runner` invokes the protector with `/protect`, with
  context cancellation, streamed diagnostics, temporary license installation and
  restoration, and cleanup of temporary files and output.
- **Environment-agnostic.** The `Runner` knows nothing about Wine. Running on a
  non-Windows host is supplied by a pluggable `Environment` (see below), so the
  package itself has no external dependencies — standard library only.

## Requirements

- Go 1.24.2 or newer.
- To *execute* protection: a Themida installation and a valid `TMLicenseA1.dat`
  license. On a non-Windows host, also an `Environment` implementation such as
  [go-wine](https://github.com/Rubberbandits/go-wine) and the tools it needs.
  Reading and editing projects needs none of these.

## Installation

```sh
go get github.com/Rubberbandits/go-themida
```

## Edit a project

Parse a `.tmd` file, override some settings by their native key, and write it
back:

```go
package main

import (
	"os"

	themida "github.com/Rubberbandits/go-themida"
)

func main() {
	data, err := os.ReadFile("app.tmd")
	if err != nil {
		panic(err)
	}

	var project themida.Project
	if err := project.Unmarshal(data); err != nil {
		panic(err)
	}

	if err := project.Override(themida.Settings{
		"OPTION_PROTECTION_IS_ANTIDEBUG":      false,
		"OPTION_MACROS_DISABLE_AT_RVA_NUMBER": 0,
		"OPTION_EXTRA_MANIFEST_FILE":          "",
	}); err != nil {
		panic(err)
	}
	project.SoftwareName = "My application"

	result, err := project.Marshal()
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("app-overridden.tmd", result, 0o600); err != nil {
		panic(err)
	}
}
```

`Settings` values may be strings, booleans, or numbers. Explicit `false`, `0`,
and empty strings are applied as overrides, not omissions; omitted keys keep
their current values. Hexadecimal values are strings such as `"0x2C27C00A"`.
Message text uses a literal `\n` rather than an actual line break. Null, arrays,
and objects are rejected.

- `Project.Set(key, value)` updates a single setting.
- `Project.Get(key)` returns a setting's current serialized value and whether it
  exists.
- `Project.Override(settings)` applies a batch atomically.

A zero-value `Project` is a usable, empty project: populate it with `Override`
or the typed fields below and `Marshal` it without a base file.

## Typed settings

The settings this package models are also reachable as typed fields, which
interoperate with raw `Settings` overrides — both resolve to the same native
keys, typed edits win, and unmodeled settings are always preserved.

```go
project.SoftwareName = "My application"

// Protection macros: encrypt ANSI strings and map a function to a VM.
project.Options.Macro.EncryptANSIStrings = true
project.Options.Macro.Functions = map[string]themida.Macro{
	"CheckLicense": {Type: 0x00000001},
}

// Customized dialog shown when a debugger is detected.
project.Messages.Debugger.Enabled = true
project.Messages.Debugger.Icon = themida.MsgIconWarning
project.Messages.Debugger.Texts = []themida.MessageText{
	{Language: "ENGLISH", Text: "A debugger was detected."},
}

// Embed a data file with XBundler.
project.Options.XBundler.Enabled = true
project.Options.XBundler.Files = []themida.XBundlerFile{
	{Name: "%INPUT_FILE_FOLDER%\\config.dat", VirtualPath: "%APP_FOLDER%\\config.dat"},
}
```

For an indexed collection (`Macro.Functions`, `Message.Texts`,
`XBundler.Files`), a `nil` value leaves the project's existing entries untouched,
while a non-`nil` value — including an empty one — replaces them in full. When
editing such a collection through raw native keys instead, supply its count and
indexed entries together.

See the [package reference](https://pkg.go.dev/github.com/Rubberbandits/go-themida)
for every typed field and its corresponding Themida feature.

## Protect a binary

Reuse a `Runner` to apply a project to an input binary. On a non-Windows host,
set `Environment` to a layer that runs the Windows protector — for example
[go-wine](https://github.com/Rubberbandits/go-wine), configured with a dedicated
prefix:

```go
runner := themida.Runner{
	Path: "/opt/themida",
	Environment: wine.Environment{
		Config: wine.Config{Prefix: "/srv/wine/themida"},
	},
	Stdout: os.Stdout,
	Stderr: os.Stderr,
}

binary, err := runner.Protect(ctx, themida.Protection{
	ProjectFile: "app.tmd",
	Options: themida.Settings{
		"OPTION_PROTECTION_IS_ANTIDEBUG": false,
	},
	InputFile:    "build/app.exe",
	OutputFile:   "build/app-protected.exe",
	DeleteOutput: true,
})
if err != nil {
	return err
}
// binary holds the protected file; DeleteOutput also removes it from disk.
```

`ctx` is the caller's context; a `nil` context runs uncancelled. `Protect`
returns the generated bytes and never returns stale or empty output.

### Selecting the protector

Set `Path` to the Themida executable or installation directory. A directory
selects `Themida64.exe` by default; set `Arch: themida.Arch32` for `Themida.exe`
or `Arch: themida.Arch64` for `Themida64.exe`. Zero keeps the 64-bit default, and
other values are rejected. An explicit executable path takes precedence over
`Arch`.

### Execution environment

On Windows, leave `Environment` nil and the protector runs directly. Elsewhere,
set it to an implementation of the `Environment` interface, which the `Runner`
uses to prepare the subprocess environment, translate host paths to the form the
protector expects, wrap the command (for example behind a loader), and tear down
afterward:

```go
type Environment interface {
	Setup(ctx context.Context, base []string) (env []string, teardown func(context.Context) error, err error)
	TranslatePath(ctx context.Context, path string, env []string) (string, error)
	Command(protector string) (executable string, prefix []string, err error)
}
```

[go-wine](https://github.com/Rubberbandits/go-wine)'s `Environment` implements
this contract for Wine; its `Config.Prefix` must be dedicated to protection,
because teardown shuts the whole Wine server down. The interface uses only
standard-library types, so neither package imports the other.

### Licenses

Themida reads its license from a file named exactly `TMLicenseA1.dat`, located
beside the protector executable. That fixed path is a Themida requirement, not a
choice this package makes.

The **recommended practice is to place your license there as part of preparing
the installation**, before protecting anything, and leave `Runner.License` nil.
`Protect` then requires that existing regular `TMLicenseA1.dat` and leaves it
untouched. A preconfigured license is simpler and avoids any churn in the
installation directory during a run.

`Runner.License` is only a convenience for cases where the license is not
already in place — for example a read-only or shared installation image with the
license supplied separately at runtime. When set to the license bytes (a non-nil
empty license is rejected), `Protect` installs them as `TMLicenseA1.dat` for the
duration of protection and keeps them installed until after environment teardown.
Any existing license is renamed to a unique backup in the same directory and
restored afterward — on success, execution failure, and cancellation alike. If
restoration fails, the backup is retained and its path is reported for recovery.
The installation directory must be writable when supplying a license this way.

### Project sources

Specify exactly one project source — `ProjectFile`, `Project`, or `Options`
alone — plus the required `InputFile` and `OutputFile`:

- `ProjectFile` protects with a `.tmd` on disk. With no overrides it is used in
  place; otherwise a temporary copy is written beside it so project-relative
  resources still resolve.
- `Project` protects with an already-parsed project. The runner clones it, so
  the caller's value is never mutated.
- `Options` alone builds a project from scratch. Set `ProjectDirectory` to
  anchor bundled resources (it defaults to the current directory).

Temporary projects are removed on both success and failure.

### Paths, output, and cleanup

`InputFile` and `OutputFile` are required host paths, resolved before the runner
changes into the installation directory. An `Environment` translates them (and
the project path) for the protector; they are always passed as distinct
arguments, without a shell. Aliasing between the input, output, project, and
license files is rejected. Any previous output is removed before protection, and
missing, empty, or non-regular output is rejected. The output file is kept unless
`DeleteOutput` is set.

Calls on one runner are serialized through their cleanup, and a waiting call
respects cancellation. Do not copy a runner after first use or change its
configuration while it is running; coordinate separate runners that share an
installation, an environment's resources (such as a Wine prefix), or an output
path. The environment's teardown runs after every outcome, including a
path-translation failure or cancellation. `CleanupTimeout` bounds it (default 15
seconds) on an independent context, and a teardown failure rejects the binary
while preserving any execution error.

### Diagnostics and the environment

The default executor cancels the whole process tree, bounds output-pipe
draining, streams the protector's output through `Stdout`/`Stderr`, and includes
a bounded diagnostic tail in errors. Wrapped errors preserve `*exec.ExitError`
and context errors. `Env` supplies a complete base environment; nil inherits the
caller's. When an `Environment` is set, it derives the subprocess environment
from that base (for Wine, selecting the prefix and quieting the debugger).

### Custom executor

Applications with their own logging or process registry can set `Execute`, a
`CommandExecutor`. It receives an unstarted command, the job context, a step
name, an optional stdout-only capture writer, and allowed exit codes. It must
run and wait for the command, honor cancellation, capture stdout when requested,
and accept allowed codes only for normal exits. In this mode the callback owns
streaming and logging.

## Development

Use golangci-lint 2.14.0 or newer. The repository configuration enables all
current linters and all six formatters; deprecated checks use their current
replacements.

```sh
go test -race ./...
go vet ./...
golangci-lint fmt
golangci-lint run ./...
```

The tests run without a Themida installation or license. They cover parsing,
serialization, setting preservation, explicit zero values, collection edits,
duplicate keys, atomic rejection of invalid data, native and Wine execution,
streamed diagnostics, cancellation, output validation, license
installation/restoration, and cleanup.

## License

Released into the public domain under the [Unlicense](LICENSE).
