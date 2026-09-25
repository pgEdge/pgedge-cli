# pgedge command reference

This page lists the usage, description and declared flags for the root
command and every top-level command. It covers what sits outside the
modules. The module trees have their own pages:
[starfleet](starfleet.md), [starfleet byoc](starfleet-byoc.md),
[starfleet managed](starfleet-managed.md) and
[controlplane](controlplane.md).

The root command's flags are global. They are declared once here and
apply to every command in every module, so the module pages do not
repeat them.

For workflows and behavioral detail, run `pgedge llms` against your
installed binary.

<!-- BEGIN GENERATED PAGE: pgedge -->

## pgedge

**Usage:** `pgedge <command> [flags]`

Unified CLI for the pgEdge product suite

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--config string` | No |  | Path to config file (default ~/.pgedge/cli/config.yaml) |
| `--debug` | No |  | Dump HTTP requests and responses to stderr (implies --verbose) |
| `--no-color` | No |  | Disable color in text output |
| `-o, --output string` | No | `text` | Output format: text, json, yaml |
| `--profile string` | No |  | Use a configured profile (see 'pgedge profile list') |
| `-v, --verbose` | No |  | Log requests and responses to stderr, without bodies |

### pgedge completion

**Usage:** `pgedge completion <command>`

Generate and install shell completion scripts

**Example:**

```
pgedge completion install
pgedge completion install --rc-only
pgedge completion zsh > ~/.zsh/completions/_pgedge
```

#### pgedge completion bash

**Usage:** `pgedge completion bash`

Print the bash completion script

**Example:**

```
pgedge completion bash
```

#### pgedge completion fish

**Usage:** `pgedge completion fish`

Print the fish completion script

**Example:**

```
pgedge completion fish
```

#### pgedge completion install

**Usage:** `pgedge completion install [flags]`

Install shell completion for your shell

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--force` | No |  | Skip the prompt: overwrite a script, or append the --rc-only line |
| `--rc-only` | No |  | Add the one-line loader to your shell startup file and write no script |
| `--shell string` | No |  | Shell to install for: bash, zsh, fish or powershell (default: autodetect $SHELL) |
| `--write-rc` | No |  | Append the zsh fpath, PowerShell profile or --rc-only loader line without prompting |

**Example:**

```
pgedge completion install
pgedge completion install --rc-only
pgedge completion install --shell powershell
```

#### pgedge completion powershell

**Usage:** `pgedge completion powershell`

Print the powershell completion script

**Example:**

```
pgedge completion powershell
```

#### pgedge completion uninstall

**Usage:** `pgedge completion uninstall [flags]`

Remove an installed shell completion script

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--force` | No |  | Remove without prompting for confirmation |
| `--shell string` | No |  | Shell to uninstall for: bash, zsh, fish or powershell (default: autodetect $SHELL) |

**Example:**

```
pgedge completion uninstall
pgedge completion uninstall --shell zsh --force
```

#### pgedge completion zsh

**Usage:** `pgedge completion zsh`

Print the zsh completion script

**Example:**

```
pgedge completion zsh
```

### pgedge doctor

**Usage:** `pgedge doctor [flags]`

Diagnose the pgedge installation

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--no-version-check` | No |  | Skip the release lookup on api.github.com |

**Example:**

```
pgedge doctor
pgedge doctor -o json
pgedge doctor --no-version-check
```

### pgedge env

**Usage:** `pgedge env <command>`

Write a linked database's connection into .env

**Example:**

```
pgedge env pull
```

#### pgedge env pull

**Usage:** `pgedge env pull [flags]`

Write DATABASE_URL for the linked database into .env

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--file string` | No |  | File to write (default .env beside .pgedge/, or in the current folder when an ID is given) |
| `--user-type string` | No |  | Role whose credentials to use: admin, app or app_read_only (default app) |
| `--var string` | No | `DATABASE_URL` | Variable name to set |

**Example:**

```
pgedge env pull
pgedge env pull --file .env.local --user-type app_read_only
```

### pgedge inspect

**Usage:** `pgedge inspect <analysis> [flags]`

Run a read-only diagnostic against a Postgres database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--db-url string` | No |  | libpq connection string of the database to inspect (required) |

**Example:**

```
pgedge inspect table-sizes --db-url "postgresql://app:secret@db.example:5432/app?sslmode=require"
pgedge inspect unused-indexes --db-url "$DATABASE_URL" -o json
```

### pgedge llms

**Usage:** `pgedge llms [module|page] [sub-reference]`

Print the AI-agent reference

**Example:**

```
pgedge llms
pgedge llms inspect
pgedge llms starfleet
pgedge llms starfleet byoc
pgedge llms starfleet byoc database mcp
```

### pgedge profile

**Usage:** `pgedge profile <command>`

Manage named CLI profiles

**Example:**

```
pgedge profile list
pgedge profile show
pgedge profile use prod
```

#### pgedge profile list

**Usage:** `pgedge profile list`

List configured profiles

**Example:**

```
pgedge profile list
pgedge profile list -o json
```

#### pgedge profile show

**Usage:** `pgedge profile show [name]`

Show a profile's connection settings

**Example:**

```
pgedge profile show
pgedge profile show prod
pgedge profile show prod -o json
```

#### pgedge profile use

**Usage:** `pgedge profile use <name>`

Switch the active profile

**Example:**

```
pgedge profile use prod
pgedge profile use prod -o json
```

### pgedge self

**Usage:** `pgedge self <command>`

Manage the pgedge binary itself

**Example:**

```
pgedge self update
```

#### pgedge self update

**Usage:** `pgedge self update [flags]`

Update the pgedge binary to the latest release

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--check` | No |  | Report whether an update is available; download nothing |
| `--force` | No |  | Skip the confirmation prompt |
| `--version string` | No |  | Update to a specific release tag instead of the newest |

**Example:**

```
pgedge self update
pgedge self update --check
pgedge self update --version v0.6.0 --force
```

### pgedge version

**Usage:** `pgedge version`

Print the pgedge CLI version

**Example:**

```
pgedge version
pgedge version -o json
```

<!-- END GENERATED PAGE -->
