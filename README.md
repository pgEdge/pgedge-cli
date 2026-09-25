<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/pgedge-labs-dark.svg">
    <img alt="pgEdge Labs" src="docs/img/pgedge-labs-light.svg" width="320">
  </picture>
</div>

# pgEdge CLI

![CI](https://github.com/pgEdge/pgedge-cli/actions/workflows/ci.yml/badge.svg)

With `pgedge`, you work with the pgEdge product suite from one
command-line interface. Its command shape is
`pgedge <module> <resource> <verb>`.

The CLI has two modules:

- `starfleet`: pgEdge Starfleet authentication and account resources,
  plus the nested `byoc` and `managed` infrastructure sub-trees.
- `controlplane`: the Control Plane.

## Installing the CLI

pgEdge publishes each release on
[GitHub Releases](https://github.com/pgEdge/pgedge-cli/releases). To
ask a question or report a problem, open a
[GitHub issue](https://github.com/pgEdge/pgedge-cli/issues).
[Getting started](docs/getting-started.md) covers your first steps.

### Handing the Install to an AI Agent

A coding agent can install the CLI for you, set up shell completion,
and add the agent skills for the products you use. The agent asks you
which products those are along the way. The install needs `curl`, plus
Node.js for the skills step. If the CLI install fails, the agent
stops. If the completion or skills step fails, or you decline it, the
agent carries on. Paste this prompt into your coding agent, such as
Claude Code or Cursor:

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

### Installing with the Script (Linux, macOS)

The install script downloads the latest release for your platform and
verifies its checksum. When `cosign` is installed, the script also
verifies the release's cosign signature. It installs to
`/usr/local/bin`, and falls back to `~/.local/bin`. Run the script:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh

To install one release instead of the newest, set `PGEDGE_VERSION`
to its tag:

    curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | PGEDGE_VERSION=<release-tag> sh

### Installing in GitHub Actions

On Linux and macOS runners, the action installs the release that its
own tag names, and verifies that release's signature. Add this step to
your workflow:

    - uses: pgEdge/pgedge-cli@v0.5.0-beta.3

[CI and automation](docs/ci.md) shows a full pipeline.

### Installing with Homebrew (macOS, Linux)

Install from the pgEdge tap with one command:

    brew install pgEdge/tap/pgedge

### Installing with go install

Go builds the CLI from source at the main branch:

    go install github.com/pgEdge/pgedge-cli/cmd/pgedge@main

To build a release instead, put its tag in place of `@main`. A binary
installed this way reports its version as `dev`.

### Downloading a Release by Hand

Download the archive for your platform from
[GitHub Releases](https://github.com/pgEdge/pgedge-cli/releases),
then verify and extract it:

    # verify integrity
    sha256sum -c checksums.txt --ignore-missing

    # verify authenticity (optional, requires cosign)
    cosign verify-blob \
        --bundle checksums.txt.sigstore.json \
        --certificate-identity-regexp \
            '^https://github\.com/pgEdge/pgedge-cli/\.github/workflows/release\.yml@refs/tags/' \
        --certificate-oidc-issuer \
            'https://token.actions.githubusercontent.com' \
        checksums.txt

    tar -xzf pgedge_*.tar.gz
    install pgedge /usr/local/bin/

pgEdge publishes builds for `linux` and `darwin` (amd64, arm64) and
`windows` (amd64, arm64). Archive names are lowercase and use Go's
spelling for each architecture. The output of `uname` maps to those
names irregularly across platforms and shells, so take the archive
name from the releases page. Windows archives are `.zip`, and all
others are `.tar.gz`.

### Building from Source

Build the binary in a checkout and run it:

    make build
    ./pgedge --help

## Updating the CLI

A downloaded release binary updates itself:

    pgedge self update           # verify the newest release and swap it in
    pgedge self update --check   # just say whether one exists

The command checks the download against the release's Sigstore
signature and checksum. It does this before the download touches the
installed binary. Afterward, it regenerates an installed completion
script. A Homebrew install or a binary inside a git checkout stays in
place, and the command names the one to run instead:
`brew upgrade pgedge` or `make build`. `pgedge doctor` also reports
the latest release. [Updating the CLI](docs/updating.md) has the
details.

## Setting Up Shell Completion

Enable Tab completion for commands, subcommands and flags:

    pgedge completion install

The command detects your shell: bash, zsh, fish or PowerShell. The
install script runs it for you, and a Homebrew install ships
completion for bash, zsh and fish. The command sets up completion in
one of two ways:

- **File install (default).** It writes a completion script into the
  directory your shell loads completions from. Then it prints any
  step that remains, such as a zsh `fpath` line or a PowerShell
  profile line. Your shell loads the script like any other completion
  file. `pgedge self update` regenerates the script, so it keeps up
  with the binary.
- **rc line (`--rc-only`).** It adds one line to your shell's startup
  file in place of the script. The installed binary makes the
  completions at every shell start, so they always match it.

To take the second route, run:

    pgedge completion install --rc-only

To add the line by hand instead, use the one for your shell:

    # ~/.bashrc
    eval "$(pgedge completion bash)"

    # ~/.zshrc
    eval "$(pgedge completion zsh)"

    # ~/.config/fish/config.fish
    pgedge completion fish | source

    # PowerShell $PROFILE
    pgedge completion powershell | Out-String | Invoke-Expression

`pgedge completion uninstall` removes whichever of the two is in
place.

To wire up a shell by hand, print its script on its own:

    pgedge completion fish
    pgedge completion powershell

Release archives also ship a `completions/` directory with a script
for each shell. It includes `pgedge.ps1` for Windows zip users who
prefer to wire it up by hand.

## Installing the AI-Agent Skills

pgEdge ships agent skills in the `skills/` directory of this
repository. Each skill is a SKILL.md file in the Agent Skills open
standard. The directory holds five skills:

- `skills/pgedge`: an orientation to the pgedge CLI, and which module
  to choose
- `skills/pgedge-starfleet`: pgEdge Starfleet authentication, invites
  and team memberships
- `skills/pgedge-byoc`: pgEdge Starfleet BYOC resources
- `skills/pgedge-managed`: pgEdge Starfleet Managed databases, which
  pgEdge hosts
- `skills/pgedge-controlplane`: pgEdge Control Plane resources

Skills are a layer above the CLI, so they install separately.

One command installs all five and detects your agent:

    npx skills add pgEdge/pgedge-cli

The command writes to `.agents/skills/`, the shared location that
supporting agents read. For agents with their own convention, it adds
a symlink. That way Claude Code, Cursor, Copilot, Amp, Antigravity and
a dozen others pick up the skills from a single install. Project scope
is the default. It puts the skills in the repository, so teammates and
cloud agents share the setup. For a per-user install, add `--global`.
Afterward, `npx skills list`, `update` and `remove` manage the skills.
`skills-lock.json` pins each one by content hash.

To install a single skill:

    npx skills add pgEdge/pgedge-cli --skill pgedge-managed

To install without Node.js, copy the skills by hand. This works with
any agent:

    git clone https://github.com/pgEdge/pgedge-cli
    mkdir -p .agents/skills
    cp -r pgedge-cli/skills/* .agents/skills/

To install for Claude Code alone, use `~/.claude/skills/` in place of
`.agents/skills/`.

## Running the Quickstart

Print the command tree, then sign in:

    pgedge --help
    pgedge starfleet auth login

## Finding the Documentation

The `docs/` tree holds the guides and the generated command reference:

- [Getting started](docs/getting-started.md)
- [Authentication and profiles](docs/auth-and-profiles.md)
- [CI and automation](docs/ci.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Versions, uninstall and support](docs/support-versioning-and-uninstall.md)
- Guides for each module: [pgEdge Starfleet](docs/starfleet/),
  [pgEdge Starfleet Managed](docs/managed/),
  [pgEdge Starfleet BYOC](docs/byoc/) and
  [the Control Plane](docs/controlplane/)
- Reference pages for each module, in [docs/reference/](docs/reference/)

AI agents should run `pgedge llms` instead. It prints the same
reference, which is embedded in the binary.

## Checking the Version

`pgedge version` reports the launcher version, plus one version for
each module. What its first line reads depends on how the binary was
built.
[Versions, uninstall and support](docs/support-versioning-and-uninstall.md)
has the details.

## Uninstalling the CLI

`pgedge completion uninstall` removes the shell completion script.
[Versions, uninstall and support](docs/support-versioning-and-uninstall.md)
lists everything else the CLI put on the machine, and where.

## Developing the CLI

Build, test and lint the checkout:

    make build test lint

See CONTRIBUTING.md.
