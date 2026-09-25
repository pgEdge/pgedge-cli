# Getting Started

This guide shows you how to install the `pgedge` CLI and run your first
command. Each release is published on
[GitHub Releases](https://github.com/pgEdge/pgedge-cli/releases).

## Installing the Binary

On Linux or macOS, the install script is the quickest route. It needs
only `curl`:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh

The script detects your platform and downloads the newest release. It
verifies the archive's checksum. When `cosign` is installed, it also
verifies the release signature. The script installs to `/usr/local/bin`.
When it cannot write there, it installs to `~/.local/bin` instead.
Then it sets up shell completion, unless the `CI` environment variable
is set. If the install directory is missing from your PATH, the script
prints the `export PATH=...` line to add.

To install one release rather than the newest, set `PGEDGE_VERSION`
to its tag from the releases page:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | PGEDGE_VERSION=<release-tag> sh

The [CI and automation](ci.md) guide pins a release this way in each
pipeline.

On Windows, download the zip for your architecture (amd64 or arm64)
from the [releases page](https://github.com/pgEdge/pgedge-cli/releases).
To verify an archive, follow the README's
"Downloading a Release by Hand" section.

Before you go further, confirm that the binary is on your PATH and
runs:

    pgedge version

Six other routes exist, and one of them hands the whole job to your AI
agent. [Other Ways to Install](#other-ways-to-install) below
describes each one and what it needs.

## Running Your First Commands

Sign in once per profile. Login prompts for the client ID and secret.
For unattended use, pass `--client-id` and `--client-secret` instead.
Login caches a token and saves the secret in the OS keychain. It saves
the rest of the profile in `~/.pgedge/cli/config.yaml`:

    pgedge starfleet auth login --profile dev

This login creates the `dev` profile. Login is the one command that
accepts a new `--profile` name. Every other command accepts only a
configured profile name, or the built-in `default`.

One credential pair serves the whole `starfleet` module. That covers
its account-level commands, byoc and managed, because all three share
one connection and one token.

To check the installation and the connection, run:

    pgedge doctor
    pgedge starfleet doctor --profile dev

Then run a real command. To pick the output format, add
`-o text|json|yaml`:

    pgedge starfleet tenant list --profile dev
    pgedge profile list

That is the whole loop: install, log in, run a command. The
[Output formats and paging](output-and-paging.md) guide covers the
three formats. The [Exit codes](exit-codes.md) guide explains what a
non-zero status means.

## Setting Up Shell Completion

Shell completion is optional. It covers bash, zsh, fish and
PowerShell. To install it, run:

    pgedge completion install

That command writes a completion script into the directory your shell
loads completions from.

The `--rc-only` flag is the other route. It appends one line to your
shell's startup file in place of the script. Each time your shell
starts, it asks the installed binary for completions, so they always
match that binary:

    pgedge completion install --rc-only

To remove completion after either route, run
`pgedge completion uninstall`.

## Setting Up Your AI Agent

Install the pgedge skills into the agent you use. Claude Code, Cursor,
Copilot, Amp and a dozen others read the same location:

    npx skills add pgEdge/pgedge-cli

This command clones the repository itself, so it also works when you
installed the binary with `go install` or from a release archive.

Project scope is the default. It puts the skills in the repository you
run the command from, so teammates and cloud agents share the setup.
To install for your user instead, add `--global`. The README's
"Installing the AI-Agent Skills" section covers updating and removing
the skills. It also covers the manual-copy path for agents without
Node.

## Reading the AI-Agent Reference

`pgedge llms` prints the machine-readable reference that ships inside
the binary. It covers global flags, profiles and exit codes. It also
carries routing tables of the modules and of the top-level command
pages, such as `pgedge llms inspect`.

`pgedge llms <module>` prints one module's index. That index routes
on to one page per resource, such as `pgedge llms starfleet byoc
cluster`. Point an AI agent at these pages first, because they are
generated from the live command tree.

In a checkout, agents should also read `skills/pgedge/SKILL.md`. It is
the entry point for the bundled Claude Code skills.

## Other Ways to Install

There are six routes in all, and each one gives you something
different. Only `go install` and a clone-and-build need Go. This table
lists what each route needs:

| Install path | Also needs |
|---|---|
| Hand it to your AI agent | Node.js, for the agent-skills step |
| Homebrew | Homebrew, on macOS |
| GitHub Actions | A Linux or macOS runner |
| Download a release archive | `tar` and `sha256sum` or `shasum` |
| `go install` from main | Go 1.26 or newer |
| Clone and build | Go 1.26 or newer |

On macOS, `brew install node` and `brew install go` cover those. Only
`npx skills add` uses Node. The README's manual-copy path installs the
skills by hand.

To download a release archive, follow the README's
"Downloading a Release by Hand" section. To ask a question or report a
problem, open a [GitHub issue](https://github.com/pgEdge/pgedge-cli/issues).

### Handing the Install to Your AI Agent

Paste the prompt below into your coding agent, such as Claude Code or
Cursor. The agent runs the install script and sets up shell
completion. It then asks which products you use, and adds the agent
skills for them. The only prerequisites are `curl`, and Node.js for
the skills step.

If the CLI install fails, the agent stops. If a completion or skills
step fails, or you decline it, the agent carries on:

<!-- install-prompt: begin -->
<!-- This block is duplicated between README.md and
     docs/getting-started.md: GitHub renders one and MkDocs the other,
     and no include mechanism serves both. TestInstallPromptCopiesMatch
     holds the two byte-for-byte, so edit both, or neither. -->

    Install the pgEdge CLI on this machine and set up its agent skills.
    Follow these steps exactly, in order, and do not improvise an
    alternative to any of them. Step 2 installs the CLI: if it fails,
    stop and show me the error. Steps 3 and 4 are optional: if one
    fails, or I decline it, tell me why and carry on with the next step.

    1. Preflight: check `curl` and `npx` exist. If `curl` is missing,
       stop and tell me it is needed for step 2. If `npx` is missing,
       tell me Node.js is needed for step 4, and skip step 4.
    2. Install the CLI (no sudo, ever): run
       `curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh`.
       The script verifies the release before installing it. Its
       `Installed pgedge <tag> to <path>` line names the binary's full
       path: use that path for `pgedge` in the rest of these steps. If
       the script warns that the directory is not on my PATH, append the
       `export PATH=...` line it prints to the rc file of my login shell
       (`~/.zshrc` for zsh, `~/.bashrc` for bash, creating it if missing,
       and skipping if the line is already there), and tell me you did.
    3. Shell completion: the script runs `pgedge completion install`,
       unless `CI` is set. If its output says a line still needs adding
       to my rc file, add that line once.
    4. Ask me this question and wait for my answer. Do not guess it:
       "Which pgEdge products do you use: Starfleet, Control Plane,
       both, or neither?" For neither, skip this step. Otherwise install
       the matching agent skills into the current project:
       - starfleet: `npx -y skills add pgEdge/pgedge-cli -s pgedge -s pgedge-starfleet -s pgedge-byoc -s pgedge-managed -y`
       - control plane: `npx -y skills add pgEdge/pgedge-cli -s pgedge -s pgedge-controlplane -y`
       - both: `npx -y skills add pgEdge/pgedge-cli -y`
    5. Verify, and show me the output of each: `pgedge version` and
       `pgedge doctor` (both at the full path from step 2), and
       `npx -y skills list` if step 4 ran. A doctor warning about
       missing credentials is expected on a fresh install. List each
       step you skipped, with the command that completes it later. The
       next step after this setup is `pgedge starfleet auth login`
       (starfleet) or reading `pgedge llms controlplane` (control
       plane).
<!-- install-prompt: end -->

### Installing with Homebrew

On macOS, install from the pgEdge tap:

    brew install pgEdge/tap/pgedge

To update, run `brew upgrade pgedge`.

### Installing with GitHub Actions

Add the action as a step. It installs the release that its tag names:

    - uses: pgEdge/pgedge-cli@v0.5.0-beta.3

The action verifies the release signature and checksum before it
installs. To install another release, set the action's `version`
input to that release's tag.

### Installing with `go install` from Main

One command installs the binary, and the same command updates it
later:

    go install github.com/pgEdge/pgedge-cli/cmd/pgedge@main

The binary lands in `$(go env GOPATH)/bin`, usually `~/go/bin`. That
directory must be on your PATH. On this route, `pgedge version`
reports `dev`, because version stamping happens in the Makefile build
below.

### Cloning and Building

This route needs the Go toolchain and make. It leaves you the checkout
as well as the binary:

    git clone https://github.com/pgEdge/pgedge-cli
    cd pgedge-cli
    make build
    install pgedge /usr/local/bin/

If you cannot write to `/usr/local/bin`, run the `install` command with
`sudo`, or install to a directory you own on your PATH, such as
`~/.local/bin`.

This build stamps `pgedge version` with the real commit and the
per-module versions. The checkout carries the AI-agent skills in
`skills/`. To update, run `git pull && make build`.

## Keeping the CLI Up to Date

A binary you downloaded from a release updates itself:

    pgedge self update

The command looks up the newest release on GitHub. Before it touches
the installed binary, it verifies the release's Sigstore signature and
the archive's checksum. Then it swaps the new binary into place.

To see whether an update exists, run `pgedge self update --check`. It
reports the answer and leaves the installed binary in place. The
"Latest version" row of `pgedge doctor` reports the same answer.

`self update` refuses a Homebrew install. For that install, run
`brew upgrade pgedge`.

The [Updating the CLI](updating.md) guide covers the rest. That
includes the two-rung GitHub lookup, the exit codes and deadlines, and
where the CLI caches the Sigstore trust root. It also names the two
installs the command refuses, and says what happens to your completion
scripts after a swap.

## Running in CI and Containers

Set `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` from your pipeline's
secret store. Every invocation then authenticates from that pair and
leaves the disk as it found it.

The [CI and automation](ci.md) guide covers the pair in CI, Docker and
Kubernetes. It covers skipping prompts with `--force`, and scripting
against the exit-code contract. It also carries two complete pipelines
you can copy.

## Next Steps

These guides pick up where this one ends:

- The [Authentication and profiles](auth-and-profiles.md) guide covers
  how credentials resolve and what a profile holds. It also covers how
  the token cache is bound to the connection that minted it.
- The
  [Connecting an Application to a pgEdge Starfleet Managed Database](managed/connect-an-application.md)
  and
  [Connecting an Application to a pgEdge Starfleet BYOC Database](byoc/connect-an-application.md)
  guides cover getting a database's host, port and credentials out of
  the CLI and into an application.
- The
  [Linking a Project Folder to a pgEdge Starfleet Managed Database](managed/link-a-project.md)
  guide covers writing a managed database's `DATABASE_URL` into your
  project's `.env`.
- The [CI and automation](ci.md) guide covers unattended runs. It
  carries worked GitHub Actions and GitLab CI pipelines.
- The [Telemetry and privacy](telemetry-and-privacy.md) guide covers
  what the binary contacts over the network. It also covers what the
  binary writes to local disk.
