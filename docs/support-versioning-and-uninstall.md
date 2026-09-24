# Versions, uninstall and support

One binary carries several modules, so "which version" has more than
one answer. This page covers what the version command reports, how to
take the CLI back off a machine, and where to go when something is
wrong.

## What the version command reports

`pgedge version` prints the launcher version on its first line, then
the commit and build date it was stamped with, then one line per
module the binary carries:

    pgedge version

Modules are versioned independently of the launcher and of each other,
so each one tracks the cadence of the service behind it. A module
version matching the launcher version is a coincidence, not a rule.

`pgedge version -o json` reports the same information as a single JSON
object.

### The first line by install route

The first line is not one shape, so do not parse it as one.

The following table describes what each route reports:

| Install route | First line |
|---|---|
| A release archive, including the one the install script downloads | The release tag with its `v` prefix stripped. |
| `make build` inside a git checkout | `git describe` output, which keeps the `v` and adds a commit or a `-dirty` suffix when the checkout is not exactly on a tag. |
| `go install` | `dev`, for the launcher and for every module. |

A `go install` build reports `dev` because the stamping happens in the
Makefile and the release build rather than in the source, so that
binary has no tag to read.

`pgedge doctor` reports the running version and the newest release
side by side, so one health check answers both what you are on and
whether you are behind. [Health checks](doctor.md) covers the rest of
that report, and [updating the CLI](updating.md) covers moving to a
newer release.

## Uninstalling

Removing the CLI mirrors installing it. One command handles the shell
completion script, and the remaining pieces come off by hand.

The completion script the CLI installed is removed by the command that
installed it:

    pgedge completion uninstall

The command prompts before removing anything, and `--force` skips that
prompt. It works out which shell you are on from `SHELL`, and `--shell`
names one instead. Running it a second time is safe.

Remove whichever of the remaining pieces your install has:

- the binary, from wherever it was installed:
  `/usr/local/bin/pgedge`, `~/.local/bin/pgedge`,
  `$(go env GOBIN)/pgedge`, or the Homebrew prefix, which
  `brew uninstall pgedge` clears for you.
- the completion script, if the binary went first and
  `pgedge completion uninstall` can no longer run:
  `~/.local/share/bash-completion/completions/pgedge` for bash,
  `~/.zsh/completions/_pgedge` for zsh,
  `~/.config/fish/completions/pgedge.fish` for fish, or
  `pgedge.complete.ps1` next to your PowerShell profile.
- the startup-file line, if the completion install told you to add
  one: the `fpath=(~/.zsh/completions $fpath)` line in `~/.zshrc`, or
  the dot-source line in your PowerShell profile.
- the agent skills, if you installed them. `npx skills add` wrote them
  to `.agents/skills/` in the project (or under your home directory
  for a global install) with a `skills-lock.json` beside them, and
  `npx skills remove pgedge pgedge-starfleet pgedge-byoc pgedge-managed
  pgedge-controlplane` takes them out again. A copy placed by hand
  under `~/.claude/skills/` is deleted by hand.
- `~/.pgedge/cli`, which holds the config file and the cached tokens.
  Deleting the directory signs you out and drops every profile.

Leave `~/.pgedge` itself in place. Other pgEdge tools share that
directory, and the CLI owns only the `cli` subdirectory inside it.

## Getting help

Work down from the cheapest answer.

The [troubleshooting guide](troubleshooting.md) is organized by the
exit code the command returned, so start from the code you got.
`pgedge doctor` reports the state of the installation itself, and it
authenticates against nothing and dials no product API, which makes it
safe to run precisely when a connection is broken. [Health
checks](doctor.md) explains each row it prints.

If you are a registered user and need help with a pgEdge product
rather than with the CLI, visit
[support.pgedge.com](https://support.pgedge.com/_hcms/mem/login?redirect_url=https%3A%2F%2Fsupport.pgedge.com%2Fcases).

To report a problem with the CLI itself, open an issue at
[github.com/pgEdge/pgedge-cli/issues](https://github.com/pgEdge/pgedge-cli/issues).

A report is easier to act on when it carries:

- the command you ran, with any credential removed.
- the exit code it returned and the line it printed to `stderr`.
- the output of `pgedge version -o json`.
- the output of `pgedge doctor -o json`, which reports the install
  method, the config file in use and every configured connection.
  Credential presence is reported, never a credential value.
- the output of `--verbose` if the problem involves a request, which
  logs requests and responses to `stderr` with the `Authorization`
  header masked.
