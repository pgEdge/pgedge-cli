# pgedge starfleet command reference

This page lists the usage, description and declared flags for the
starfleet module's account-level commands: authentication, API
clients, invites, memberships and tenants. The module's two
infrastructure sub-trees have their own pages:
[starfleet byoc](starfleet-byoc.md) and
[starfleet managed](starfleet-managed.md).

The connection flags declared on `pgedge starfleet` are inherited by
every command in the tree, the two sub-trees included: one product
means one connection, and a single `pgedge starfleet auth login` serves
them all. Global flags are declared on the root command and listed on
the [pgedge reference page](pgedge.md).

For workflows and behavioral detail, run `pgedge llms starfleet`
against your installed binary.

<!-- BEGIN GENERATED PAGE: pgedge starfleet -->

## pgedge starfleet

**Usage:** `pgedge starfleet <command> [flags]`

Manage pgEdge Starfleet

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--api-url string` | No |  | pgEdge Starfleet API base URL (default https://api.pgedge.com) |
| `--client-id string` | No |  | API client ID (overrides profile config) |
| `--client-secret string` | No |  | API client secret (overrides profile config) |
| `--tenant string` | No |  | Tenant ID a browser sign-in acts in (overrides the profile's) |
| `--timeout duration` | No | `30s` | Per-request timeout (Go duration; 0 disables) |

**Example:**

```
pgedge starfleet auth login
pgedge starfleet byoc cluster list
pgedge starfleet managed database list
```

### pgedge starfleet api

**Usage:** `pgedge starfleet api <method> <path> [flags]`

Call any Starfleet API path over the module's connection

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-d, --data string` | No |  | JSON request body: a literal, @file, or - for stdin |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `-H, --header stringArray` | No |  | Request header as Name: value (repeatable) |
| `-i, --include` | No |  | Print the status and response headers on stderr |
| `--query stringArray` | No |  | Query parameter as key=value (repeatable) |

**Example:**

```
pgedge starfleet api GET /byoc/v1/clusters
pgedge starfleet api GET /managed/v1/databases --query limit=5 -o json
pgedge starfleet api POST /managed/v1/databases --data @spec.json --dry-run
```

### pgedge starfleet auth

**Usage:** `pgedge starfleet auth <command>`

Manage pgEdge Starfleet authentication

**Example:**

```
pgedge starfleet auth login
pgedge starfleet auth status
pgedge starfleet auth whoami
```

#### pgedge starfleet auth login

**Usage:** `pgedge starfleet auth login [flags]`

Authenticate with pgEdge Starfleet

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--api-client` | No |  | sign in with an API client, prompting for its ID and secret |
| `--client-secret-file string` | No |  | read the API client secret from this file |
| `--client-secret-stdin` | No |  | read the API client secret from standard input |
| `--insecure-storage` | No |  | store the client secret in the config file, not the OS keychain |

**Example:**

```
pgedge starfleet auth login
pgedge starfleet auth login --tenant TENANT_ID
pgedge starfleet auth login --client-id ID --client-secret-file FILE
pgedge starfleet auth login --client-id ID --client-secret-stdin < FILE
pgedge starfleet auth login --api-client --insecure-storage
```

#### pgedge starfleet auth logout

**Usage:** `pgedge starfleet auth logout`

Clear stored pgEdge Starfleet credentials and token

**Example:**

```
pgedge starfleet auth logout
```

#### pgedge starfleet auth status

**Usage:** `pgedge starfleet auth status [flags]`

Show current authentication state

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--verify` | No |  | Check that the server accepts the credential |

**Example:**

```
pgedge starfleet auth status
pgedge starfleet auth status -o json
pgedge starfleet auth status --verify
```

#### pgedge starfleet auth whoami

**Usage:** `pgedge starfleet auth whoami`

Show the identity the API accepts you as

**Example:**

```
pgedge starfleet auth whoami
pgedge starfleet auth whoami -o json
```

### pgedge starfleet client

**Usage:** `pgedge starfleet client <command>`

Manage pgEdge Starfleet API clients

**Example:**

```
pgedge starfleet client list
pgedge starfleet client create --name ci --description "CI runner"
```

#### pgedge starfleet client create

**Usage:** `pgedge starfleet client create [flags]`

Create an API client

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--description string` | Yes |  | What this client is for |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | Yes |  | Name for the new API client |
| `--secret-file string` | No |  | Write the client secret to this new file instead of stdout |

**Example:**

```
pgedge starfleet client create --name ci --description "CI runner"
pgedge starfleet client create --name ci --description "CI runner" \
  --secret-file ci.secret
pgedge starfleet client create --name ci --description "CI runner" \
  -o json
```

#### pgedge starfleet client delete

**Usage:** `pgedge starfleet client delete <client_id> [flags]`

Delete an API client

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet client delete b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet client delete b0c1d2e3-f4a5-6789-bcde-890123456789 --force
```

#### pgedge starfleet client get

**Usage:** `pgedge starfleet client get <client_id>`

Show API client details

**Example:**

```
pgedge starfleet client get b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet client get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml
```

#### pgedge starfleet client list

**Usage:** `pgedge starfleet client list`

List API clients

**Example:**

```
pgedge starfleet client list
pgedge starfleet client list -o json
```

#### pgedge starfleet client update

**Usage:** `pgedge starfleet client update <client_id> [flags]`

Update an API client

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--description string` | No |  | New description for the API client |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | No |  | New name for the API client |

**Example:**

```
pgedge starfleet client update b0c1d2e3-f4a5-6789-bcde-890123456789 \
  --name ci-runner
pgedge starfleet client update b0c1d2e3-f4a5-6789-bcde-890123456789 \
  --description "Nightly CI"
```

### pgedge starfleet doctor

**Usage:** `pgedge starfleet doctor [flags]`

Diagnose the pgEdge Starfleet connection

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--strict` | No |  | Exit non-zero unless the verdict is ready |

**Example:**

```
pgedge starfleet doctor
pgedge starfleet doctor -o json
pgedge starfleet doctor --strict
```

### pgedge starfleet invite

**Usage:** `pgedge starfleet invite <command>`

Manage pgEdge Starfleet team invites

**Example:**

```
pgedge starfleet invite list
pgedge starfleet invite create --email teammate@example.com
```

#### pgedge starfleet invite accept

**Usage:** `pgedge starfleet invite accept <invite_id> [flags]`

Accept an invite (needs the web UI)

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--token string` | No |  | Invite token |

**Example:**

```
pgedge starfleet invite list
```

#### pgedge starfleet invite create

**Usage:** `pgedge starfleet invite create [flags]`

Create an invite (needs the web UI)

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--email string` | No |  | Email address to invite |
| `--expiration int` | No |  | Invite expiration in hours (optional) |

**Example:**

```
pgedge starfleet invite list
```

#### pgedge starfleet invite delete

**Usage:** `pgedge starfleet invite delete <invite_id> [flags]`

Delete an invite

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet invite delete b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet invite delete b0c1d2e3-f4a5-6789-bcde-890123456789 --force
```

#### pgedge starfleet invite get

**Usage:** `pgedge starfleet invite get [<invite_id>] [flags]`

Show invite details

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--email string` | No |  | Find the invites for this email address instead of an ID |

**Example:**

```
pgedge starfleet invite get b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet invite get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml
pgedge starfleet invite get --email teammate@example.com
```

#### pgedge starfleet invite list

**Usage:** `pgedge starfleet invite list`

List invites

**Example:**

```
pgedge starfleet invite list
pgedge starfleet invite list -o json
```

### pgedge starfleet membership

**Usage:** `pgedge starfleet membership <command>`

Manage pgEdge Starfleet team memberships

**Example:**

```
pgedge starfleet membership list
pgedge starfleet membership delete b0c1d2e3-f4a5-6789-bcde-890123456789
```

#### pgedge starfleet membership delete

**Usage:** `pgedge starfleet membership delete <membership_id> [flags]`

Remove a team member

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet membership delete b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet membership delete b0c1d2e3-f4a5-6789-bcde-890123456789 --force
```

#### pgedge starfleet membership list

**Usage:** `pgedge starfleet membership list [flags]`

List team memberships

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--email string` | No |  | Show only the members with this email address |

**Example:**

```
pgedge starfleet membership list
pgedge starfleet membership list -o json
pgedge starfleet membership list --email teammate@example.com
```

### pgedge starfleet tenant

**Usage:** `pgedge starfleet tenant <command>`

Manage your pgEdge Starfleet tenant

**Example:**

```
pgedge starfleet tenant list
pgedge starfleet tenant update b0c1d2e3-f4a5-6789-bcde-890123456789 --name acme
```

#### pgedge starfleet tenant get

**Usage:** `pgedge starfleet tenant get <tenant_id>`

Show tenant details

**Example:**

```
pgedge starfleet tenant get b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet tenant get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml
```

#### pgedge starfleet tenant list

**Usage:** `pgedge starfleet tenant list`

List tenants

**Example:**

```
pgedge starfleet tenant list
pgedge starfleet tenant list -o json
```

#### pgedge starfleet tenant update

**Usage:** `pgedge starfleet tenant update <tenant_id> [flags]`

Rename a tenant

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | No |  | New name for the tenant |

**Example:**

```
pgedge starfleet tenant update b0c1d2e3-f4a5-6789-bcde-890123456789 --name acme
```

<!-- END GENERATED PAGE -->
