# AI agents

Two different jobs sit under this heading, and they meet the CLI at
opposite ends. An agent can drive `pgedge` itself, running commands
and reading what comes back, which is a question of handing the agent
an accurate description of the CLI. Separately, `pgedge` deploys
pgEdge's own MCP server alongside a database, which gives an agent a
query interface over that database and involves the CLI only at deploy
time.

## Let an agent drive the CLI

An agent composing commands from `--help` output is guessing. The CLI
ships two things that stop it guessing: a machine-readable reference
inside the binary, and a set of agent skills.

### The reference inside the binary

`pgedge llms` prints the index, which covers what holds across the
whole CLI: global flags, configuration and profiles, environment
variables, output formats and exit codes, with tables routing to each
module's own reference and to a page per top-level command group
(`pgedge llms inspect`). Point an agent at the
index first, because it is the read that tells the agent which other
read it needs.

`pgedge llms <module>` prints one module's index: what holds across the module,
then a Pages table routing to one page per resource. The page's path is the
command's path, so `pgedge llms starfleet byoc cluster` documents `pgedge
starfleet byoc cluster` and its commands, and the `database` page routes once
more to its sub-resources (`pgedge llms starfleet managed database mcp`). The
`starfleet` module carries two product sub-trees, `byoc` and `managed`, each
with an index of its own. An agent reaches any command in three short reads,
four for a sub-resource of a product sub-tree, and never loads a resource it is
not working on.

The whole reference ships inside the binary, so an installed copy
prints it with no checkout and no network. A module name the binary
does not carry is a usage error at exit 2, and the message names the
modules it does carry.

The same content is on this site under
[command reference](reference/pgedge.md), generated from the same
command tree. An agent with a terminal should prefer `pgedge llms`,
since it describes the binary actually installed.

### The agent skills

pgEdge ships five agent skills, written to the Agent Skills open
standard, in the `skills/` directory of the repository. The following
table describes what each one covers:

| Skill | Covers |
|---|---|
| `pgedge` | CLI orientation, choosing a module, profiles and installation. |
| `pgedge-starfleet` | Starfleet authentication, API clients, team invites and memberships. |
| `pgedge-byoc` | BYOC clusters, databases, services, ingresses, backups and SSH keys. |
| `pgedge-managed` | Managed databases, their services and their backups. |
| `pgedge-controlplane` | A self-hosted Control Plane: cluster formation, databases, backups and failover. |

One command installs all five and detects your agent:

    npx skills add pgEdge/pgedge-cli

It writes to `.agents/skills/`, the shared location supporting agents
read, and symlinks that for the agents which keep a convention of
their own, so Claude Code, Cursor, Copilot, Amp, Antigravity and a
dozen others pick the skills up from one install. Project scope is the
default, which puts them in the repository you run the command from so
teammates and cloud agents share the setup. Afterward the same tool's
`list`, `update` and `remove` commands manage them, and
`skills-lock.json` pins each one by content hash.

The `pgedge` binary does not install the skills, because skills are a
layer above the CLI. The README's "AI-agent skills" section covers
installing a single skill, installing for your user rather than a
project, and a manual copy for an agent without Node.

An agent that reads neither `.agents/skills/` nor one of the
conventions the installer symlinks is still supported. Copy the
directories under `skills/` to wherever that agent looks, which its
own documentation is the place to confirm, and nothing else about the
setup changes.

### Output an agent can parse

`-o json` and `-o yaml` render the API's own object, and the renderer
normalizes both through the same JSON tags, so a `yq` path always
matches the equivalent `jq` path. Diagnostics never enter the result,
because `--verbose` and `--debug` write to `stderr`.

Some successes carry no body. Those print nothing at all to `stdout`
in any of the three formats, and the acknowledgment is exit 0 with a
sentence on `stderr`, so an agent that treats empty `stdout` as
failure will misread them. The
[output formats and paging guide](output-and-paging.md) has the full
contract, and the [exit codes guide](exit-codes.md) covers what each
code lets an agent conclude.

## Deploy an MCP server

The MCP server runs alongside a Postgres database and gives an LLM a
query interface over it, read-only unless you grant more. Which
commands deploy one depends on the product, and the three below do not
share a recipe.

### On a managed database

The write commands live under `database mcp`, and a deploy with no flags set
yields a read-only server with an API-generated bearer token:

    pgedge starfleet managed database mcp deploy <database-id> --wait

`deploy` refuses to run when an MCP service is already deployed and
names `mcp update` as the fix. The
[deploy managed services workflow](managed/services.md)
covers every configuration flag, which of them the API treats as
secrets, and what a wait does and does not prove.

### On a BYOC database

The commands read the same, and the service runs on the cluster's own nodes in
your cloud account, so what stands in front of its endpoint is your networking
rather than pgEdge's:

    pgedge starfleet byoc database mcp deploy <database-id> --wait

On a multi-node cluster the CLI refuses to choose the placement, so
pass `--target-nodes`. The
[deploy BYOC services workflow](byoc/services.md) covers
that, the configuration flags, and the regional ingress a private
cluster needs before anything outside the VPC can reach the service.

### On a self-hosted Control Plane

The Control Plane has no `mcp` command group. A service is declared in the
database spec instead, under the spec's `services` block, and reaches the
Control Plane when the database is created:

    pgedge controlplane database init -i > storefront.yaml
    pgedge controlplane database create storefront -f storefront.yaml --wait

`database init -i` interviews you for the spec, and its guided
services step collects the service type, a service id, the connect-as
database user, host IDs and version, plus a guided LLM configuration
for an mcp service. No credential is ever written as a real value: the
provider credential lands as a `CHANGE-ME` placeholder, and `create`
refuses the spec until you replace it. The
[manage Control Plane databases workflow](controlplane/databases.md)
covers the three service shapes and the bootstrap credentials the
Control Plane accepts on create and rejects on update.

## Point an agent at a deployed server

This section is about the two Starfleet products. A Control Plane
service is configured through the spec's `config` block, whose init
token field is the bearer token, and the [Control Plane
databases guide](controlplane/databases.md) covers reading a
service back.

An MCP client needs two values, and both come from a read of a single
database rather than from anything the deploy printed.

The endpoint is the first, and the two Starfleet products report it
differently. The following table describes where each one leaves it:

| Product | The URL to give the client |
|---|---|
| Managed | The service's `uri`, exactly as reported. It already carries the version segment, so it reads `https://<domain>/mcp/v1`. |
| BYOC | ENDPOINT is a bare origin rather than a full path, so append `/mcp/v1` to it. |

Ask the API for that value rather than assembling it, because the path
segment is the platform's to choose. On managed, `service list`,
`service get` and `database get` all print it as ENDPOINT, and
`-o json` carries the field itself. On BYOC the CLI composes ENDPOINT
for the table from the service's domains, and under `-o json` a script
builds the same locator from the raw fields.

The bearer token is the second. It is whatever was passed to
`--init-tokens` at deploy time, generated by the API when that flag
was omitted, or rotated afterward with `mcp update`. A token typed as
a flag lands in your shell history and is visible in `ps` to anyone on
the same host, so let the API generate it and read it back. No table view
prints a secret in any format, so reading it needs `-o json` on a read
that hits the single database: `database get`, `service get` or
`service list` all do, and `database list` is the one that omits it.
The value sits in the `services` entry whose `service_type` is `mcp`,
under that entry's `mcp_config` block.

This reads both values for a managed database and stops if either read
fails. The file holds a working bearer token, so it is created under a
restrictive umask and removed once the values are in the client:

    umask 077
    if ! pgedge starfleet managed database get <database-id> -o json \
        > mydb.json
    then
        echo "database get failed; mydb.json holds nothing" >&2
        exit 1
    fi
    uri=$(jq -r '.services[] | select(.service_type=="mcp") | .uri' \
        mydb.json)
    token=$(jq -r '.services[] | select(.service_type=="mcp") |
        .mcp_config.init_tokens' mydb.json)
    if [ -z "$uri" ] || [ "$uri" = null ] ||
       [ -z "$token" ] || [ "$token" = null ]
    then
        echo "mydb.json carries no MCP uri or token" >&2
        exit 1
    fi

The redirection truncates the file before the command runs, so a
failed read leaves an empty file that `jq` reads happily, and a service
carrying no token yields the four characters `null`, which a client
would then send as a bearer token of that name. Re-run the read rather
than reusing the file later, because a file written for another
database produces a successful call against the wrong server with
nothing to notice. Delete the file once the client has the values:

    rm -f mydb.json

### What goes in the client's configuration

Where an agent keeps its MCP client configuration differs from agent
to agent, and its own documentation is the place to look. What goes in
that configuration does not differ:

- the URL from the table above, as a streamable-HTTP MCP server.
- an `Authorization` header carrying `Bearer` and the token.

The transport wants an `Accept` header naming both JSON and the event
stream, and the first request on a connection must be an `initialize`
call, since a cold `tools/list` is a lifecycle violation rather than a
shortcut. A client that speaks streamable HTTP handles both itself. A
server may answer with an `Mcp-Session-Id` header, which later
requests on the same session echo back.

### Readiness after a deploy

Exit 0 on a deploy means the API accepted the write, not that the
server answers. On managed the endpoint returns 503 for roughly
fifteen to twenty seconds afterward, and the service `state` field
does not close that gap: it reads `running` while the server is still
answering 503. An `initialize` call that succeeds is the honest
readiness signal, and both services workflows carry a `curl` recipe
that makes one. Point the agent at the server after that call
succeeds, not after the deploy returns.

## Query limits and write access

The two Starfleet products deploy the same server, and it behaves as
follows.

The query tool takes a `limit`, defaulting to 100 rows and topping out
at 1000, and an `offset` to continue from. The server appends both to
a query that does not already carry them, and a truncated response
says more is available and names the offset to continue from, where a
complete result of the same size reports only its row count.

All of that depends on the server supplying the limit, and the check
for an existing one is a substring test over the query text. Any
occurrence of `LIMIT`, including a column name or an alias such as
`unlimited`, stops the server appending anything and silences the
truncation notice with it. An agent should pass `limit` as a parameter
rather than writing the clause into the SQL.

Write access is off by default. On managed and BYOC, `--allow-writes`
grants the LLM insert, update and delete access through the query
tool.

## Next steps

- The [deploy managed services workflow](managed/services.md)
  covers deploying and configuring an MCP server on a managed
  database.
- The [deploy BYOC services workflow](byoc/services.md)
  covers the same on a BYOC database, which differs in enough places
  that a recipe does not carry across unchanged.
- The [manage Control Plane databases workflow](controlplane/databases.md)
  covers declaring services in a Control Plane database spec.
- The [getting started guide](getting-started.md) covers installing
  the CLI and its skills from scratch.
