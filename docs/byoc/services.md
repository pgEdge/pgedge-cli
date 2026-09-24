# Deploy BYOC services

A BYOC database can carry three services alongside Postgres. The MCP
(Model Context Protocol) server gives an LLM a query interface over
the database. The RAG (Retrieval-Augmented Generation) server answers
questions from retrieval pipelines built over the database's own
tables. PostgREST serves a REST API generated from the schema, so
exposed tables and views become endpoints with no application code.

All three run on the cluster's own nodes, in your cloud account. What
stands in front of a service endpoint is therefore your own
networking, not pgEdge's.

You need the full UUID of a database that reports `available`, and the
node names from `node list <cluster-id>` if the cluster has more than
one. The [Manage BYOC databases](databases.md) guide covers getting there.

## The commands

The read commands live under `database service`. The write commands
live under `database mcp`, `database rag` and `database postgrest`.

The following table describes the service commands:

| Command | What it does |
|---|---|
| `database service list <db>` | Lists the deployed services as SERVICE ID, TYPE, STATE and ENDPOINT. |
| `database service get <db> <service-id>` | Shows one service, addressed by its service id. |
| `database service remove <db> <type>` | Removes one service, addressed by type. Destructive, and prompts unless `--force`. |
| `database mcp deploy <db>` | Creates the MCP service. |
| `database mcp update <db>` | Reconfigures the deployed MCP service. |
| `database rag deploy <db>` | Creates the RAG service. |
| `database rag update <db>` | Reconfigures the deployed RAG service. |
| `database postgrest deploy <db>` | Creates the PostgREST service. |
| `database postgrest update <db>` | Reconfigures the deployed PostgREST service. |

A service id is an 8-character hex string the platform assigns, not a
UUID. Every other identifier on this page is a UUID, and the service
id is the exception. Read the service id from the SERVICE ID column,
or from `service_id` under `-o json`. `get` takes a service id, and
`remove` takes a type, so passing one where the other belongs does not
behave as expected.

Every write above is asynchronous and takes `--wait`, `--follow`,
`--wait-timeout` and `--wait-interval`. The reads take none of them.

## Service replacement and merging

The BYOC API treats a database's `services` field as declarative:
whatever a request sends replaces the whole list. Sending only a RAG
service therefore destroys the MCP server already deployed beside it.

The CLI never sends a partial list. Every write here reads the
database first, merges your flags into the service being changed, and
carries the other services through untouched. The write then sends
the complete list back. That read is also why a write needs a
database that is readable, and why a failure to read is a refusal to
write.

Anyone calling the API directly, rather than through this CLI, has to
do the same merge by hand.

## Deploy creates, update reconfigures

`deploy` refuses to run when a service of that type is already
deployed, and `update` refuses when none is. Both checks are
client-side, over the read the command has already made, so a refusal
sends no write at all. `deploy` exits 1 naming `update` as the fix,
and `update` exits 1 naming `deploy`.

`deploy` is therefore not idempotent. A caller that wants one command
to do either job branches on the read:

    if ! pgedge starfleet byoc database service list "$DB" -o json \
        > svc.json
    then
        echo "service list failed; deploying nothing" >&2
        exit 1
    fi
    if jq -e '.[] | select(.service_type=="mcp")' svc.json >/dev/null
    then
        pgedge starfleet byoc database mcp update "$DB" --allow-writes
    else
        pgedge starfleet byoc database mcp deploy "$DB" --allow-writes
    fi

Test the read's own exit code before the filter. A pipeline reports
its last command's status, so `pgedge ... | jq -e` reports `jq`'s.
`jq -e` exits 1 both when nothing matches and when the read failed and
printed nothing. Without the first check, a 404, an expired credential
or a network error picks the `else` branch. The branch then deploys a
second service off a read that never ran.

BYOC has no by-type read to shorten that recipe with. `service get`
addresses a service by its generated id, so checking for a type before
it has an id means filtering `service list`. Managed does have one,
which is why the
[Deploy managed services](../managed/services.md) guide branches on an
exit code instead.

## One change at a time

A database accepts one change at a time. While a previous deploy
settles, its status is `modifying`, and any further service edit fails
with `database in unmodifiable state: modifying`. Wait for
`available` between service operations, which is what `--wait` on the
first one buys you.

## Choosing the nodes

On a single-node cluster, omit `--target-nodes` and the CLI selects
the one node. On a multi-node cluster the CLI refuses to guess, and
exits 1 naming the cluster's nodes, so pass the names you want.

An `update` keeps the placement the deploy chose. Pass
`--target-nodes` again only to move a service, never merely to satisfy
the update. The
[Manage BYOC databases](databases.md) guide covers how the
flag parses a list, including the empty element that survives it.

## Secret flags

Six flags on this page carry a secret. Every one of them is a plain
string flag with no file or `stdin` alternative:
`--embedding-api-key`, `--init-tokens` and `--init-users` on MCP,
`--embedding-llm-api-key` and `--completion-llm-api-key` on RAG, and
`--jwt-secret` on PostgREST.

A value typed on the command line lands in your shell history. The
value also stays visible in `ps` to anyone on the same host for as
long as the command runs. Read each one from the environment instead,
as the examples below do. Export the value from something that is not
a shell history file:

    pgedge starfleet byoc database mcp deploy <db-id> \
        --embedding-provider openai \
        --embedding-model text-embedding-3-small \
        --embedding-api-key "$OPENAI_API_KEY" \
        --wait

Quote the variable. An unquoted `$OPENAI_API_KEY` that is unset
becomes a missing argument instead of an empty one, and the flag then
swallows whatever follows it.

## Configuring the MCP server

Every MCP flag is optional, and the API accepts a configuration with
none of them set, so a bare deploy yields a read-only server.

The following table describes the MCP flags and marks the ones the API
treats as secrets:

| Flag | Secret | Notes |
|---|---|---|
| `--allow-writes` | No | Grants the LLM insert, update and delete access through the query tool. Off by default. |
| `--embedding-provider` | No | Accepts `ollama`, `openai` or `voyage`. The CLI checks neither the name nor the model. The API refuses a wrong value instead. |
| `--embedding-model` | No | Required by the API alongside the provider. |
| `--embedding-api-key` | Yes | Required for `openai` and `voyage`. Setting either provider with no key passed or already stored fails at exit status 2 before the change is sent. On `update`, omit the flag to reuse the stored key. |
| `--ollama-url` | No | Endpoint of an Ollama server, required when the provider is `ollama`. |
| `--init-tokens` | Yes | Bearer token forwarded to the MCP server as INIT_TOKENS. |
| `--init-users` | Yes | Comma-separated `username:password` pairs forwarded as INIT_USERS. |

`--ollama-url` is one of the differences from managed. Managed has no
Ollama provider at all, because self-hosted model serving has nowhere
to run there.

`update` changes only the flags you pass and reads everything else
back from the deployed service, including the embedding provider, the
model and the node placement. MCP's secrets survive that round trip,
because the API returns all three of them on a single-database read.

`--allow-writes` is a boolean, so it cannot express "leave it alone"
through its value. Omit `--allow-writes` to keep the current access
level, pass `--allow-writes` to grant write access, and
`--allow-writes=false` to revoke it. Do not pass `--allow-writes` on an
unrelated change to be safe: that is a privilege decision, not a
no-op.

## MCP query limits

The query tool takes a `limit`, defaulting to 100 rows and topping out
at 1000, and an `offset` to continue from. The server appends both to
a query that does not already carry them. A truncated response says
more is available, and it names the offset to continue from. A
complete result of the same size reports only its row count. A
separate row-count tool returns the total, so a caller that needs more
than 100 rows asks for up to 1000.

All of that depends on the server supplying the limit, and the check
for an existing one is a substring test over the query text. Any
occurrence of `LIMIT`, including a column name or an alias such as
`unlimited`, stops the server appending anything, and silences the
truncation notice too. `Results (200 rows)` from a query carrying its
own `LIMIT 200` is then indistinguishable from a complete result. Pass
`limit` as a parameter, not as a clause written into the SQL.

## Configuring the RAG server

`rag deploy` requires seven flags: both LLM triples and a pipeline
file. The API rejects a partial configuration outright, so the CLI
refuses one first.

The following table describes the RAG flags and marks the ones the API
treats as secrets:

| Flag | Secret | Notes |
|---|---|---|
| `--embedding-llm-provider` | No | Required on deploy. |
| `--embedding-llm-model` | No | Required on deploy. |
| `--embedding-llm-api-key` | Yes | Required on deploy. Write-only, so pass it again whenever you change the provider or model it belongs to. |
| `--completion-llm-provider` | No | Required on deploy. |
| `--completion-llm-model` | No | Required on deploy. |
| `--completion-llm-api-key` | Yes | Required on deploy, and write-only in the same way. |
| `--pipeline-config` | No | Path to a JSON file holding the pipeline definitions. Required on deploy. |
| `--top-n` | No | Default number of results retrieved per pipeline. |
| `--token-budget` | No | Default maximum completion tokens across all pipelines. |

`--pipeline-config` takes a file. The file holds either a bare array
of pipelines or an object with a `pipelines` key. The smallest file
the API accepts names one pipeline over one table:

    [{"name": "docs", "tables": [
        {"table": "public.documents",
         "text_column": "content",
         "vector_column": "embedding"}]}]

The CLI validates that file before sending the write. That check runs
after the database read. The file needs at least one pipeline, a name on
each, and no duplicate names. `_default` is refused as a reserved
name. Each pipeline needs at least one table, and every table needs
`table`, `text_column` and `vector_column` set. All of those checks
are exit 2, along with an unreadable path, a path naming a directory,
and JSON that does not parse. All are the same class of mistake.
Neither the CLI nor the API checks that the tables exist.

`rag update` changes only the flags you pass, and the pipelines are
the ones to watch. `--pipeline-config` is the complete pipeline list,
so include every pipeline you want to keep. The two API keys are the
other exception: the API never returns them, so there is nothing for
the CLI to read back and merge.

## Pipeline endpoint access

WARNING: a RAG pipeline serves whatever it retrieves to whoever can
reach it. Neither the CLI nor the API offers a token, a password or
any other credential for the pipeline endpoint. MCP's `--init-tokens`
has no RAG equivalent on either product.

On managed, the pipeline endpoint answers a plain `curl` carrying no
credential of any kind. The MCP service at the same hostname answers
401 to the same request instead. On BYOC the service runs in your
cloud account, so what reaches it is decided by your cluster's
networking rather than by anything pgEdge configures.

Three consequences follow:

- a pipeline on a public cluster is reachable by anyone who learns the
  hostname. The only barrier is that the hostname is generated.
- pipeline names are enumerable on managed, because an unknown name
  answers 404 where a real one answers 200.
- every request spends your own embedding and completion credits, on
  the keys supplied at deploy time.

Establish what stands in front of the endpoint before you deploy. Do
not point a pipeline at data you would not publish at that hostname.
The
[Deploy managed services](../managed/services.md) guide covers the
managed case.

## Configuring PostgREST

PostgREST deploys on BYOC, unlike on managed, where the platform
rejects the service type outright. Two flags are required on deploy,
because they decide what is reachable and as whom.

The following table describes the PostgREST flags and marks the one
the API treats as a secret:

| Flag | Secret | Notes |
|---|---|---|
| `--db-schemas` | No | Comma-separated schemas to expose. Required on deploy. |
| `--db-anon-role` | No | Postgres role serving unauthenticated requests. Required on deploy. |
| `--db-pool` | No | Connections held open, 1 to 30, default 10. |
| `--max-rows` | No | Rows returned per request, 1 to 10000, default 1000. |
| `--cors-origins` | No | Comma-separated list of allowed origins. |
| `--jwt-secret` | Yes | Signing secret, 32 bytes or more, though the refusal counts them in characters. Write-only, so pass it again whenever you change other JWT settings. |
| `--jwt-audience` | No | Audience claim to require. |
| `--jwt-role-claim-key` | No | JSONPath to the role claim inside the JWT. |

Choose the anonymous role deliberately. The role must already exist,
and it should hold only the privileges an anonymous caller may have.
app_read_only is the safe default for public read access. Never hand
the role `admin`: on BYOC that role is a real Postgres superuser, and
the [Manage BYOC databases](databases.md) guide covers what that
means.

PostgREST's flag values are checked before the CLI resolves a
credential. A bad `--db-pool 0`, a `--max-rows` out of range, a short
`--jwt-secret`, or a missing or empty `--db-schemas` or
`--db-anon-role` all fail at exit 2. Each one sends no request and
needs no profile. The
one exception is an empty value on `update`. The CLI catches that
value after the database reads back, because the value being blanked
came from the deployed service.

RAG is checked later. `--pipeline-config` is read and validated inside
the same step that merges the flags. That step runs after the database
GET, so a bad file needs a working profile to be reported at all. With
no credentials, `rag deploy` exits 5 before the file is ever opened.
The [Exit codes](../exit-codes.md) guide covers what each code means.

## Where the secrets surface

MCP's three secrets come back from a read, and RAG's and PostgREST's
never do.

The following table describes where each one appears:

| Field | Service | Returned by |
|---|---|---|
| `mcp_config.init_tokens` | MCP | The single-database read, under `-o json` or `-o yaml`. |
| `mcp_config.init_users` | MCP | The single-database read, as above. |
| `mcp_config.embedding_api_key` | MCP | The single-database read, as above. |
| The RAG LLM API keys | RAG | Nothing. Both are write-only. |
| `postgrest_config.jwt_secret` | PostgREST | Nothing. It is write-only. |

`database get`, `service list` and `service get` all read the single
database, so all three carry MCP's secrets. `database list` is the
read that omits them. A configuration built from a list entry arrives
with its secrets blank. That is indistinguishable from a service that
has none set. No table view prints a secret in any format, so reading
one needs `-o json` or `-o yaml`.

## The endpoint to dial

ENDPOINT is the locator a caller can dial. The CLI composes ENDPOINT
for the table, instead of reading it from a field. A service with a
public domain renders as `https://<public-domain>`, because the
service sits behind TLS-terminating ingress on 443. A service with
only a private domain renders as `<private-domain>:<port>`, which is
the real locator for a caller on that private network. A service with
neither domain renders the port alone, labeled as internal so it
cannot be mistaken for an address. A service with neither domain nor
port renders an empty cell.

Under `-o json` there is no composed endpoint and no `uri` field.
`public_domain`, `private_domain` and `port` are the raw fields, and a
script builds the locator from them the same way. Do not dial `port`
on the public domain: it is the internal port behind the ingress, and
a connection to it fails with `SSL:WRONG_VERSION_NUMBER`.

ENDPOINT is a bare origin, not a full path. A deployed MCP server
speaks streamable-HTTP MCP at `POST <endpoint>/mcp/v1`, and a RAG
server serves its pipelines under `<endpoint>/rag/v1`. The client
appends the path.

A private cluster needs a regional ingress in front of a service
before anything outside the VPC can reach it. The
[Expose a BYOC service](expose-service.md) guide covers that.

## Verify a deployed MCP server

Exit 0 on a deploy means the API accepted the write, not that the
server answers. An `initialize` call that succeeds is the readiness
signal. The transport wants an `Accept` header naming both JSON and
the event stream. The first request on a connection must be
`initialize`, because a cold `tools/list` is a lifecycle violation.

This reads the domain and the token, and stops if either read fails.
The recipe then opens a session:

    if ! pgedge starfleet byoc database get "$DB" -o json > mydb.json
    then
        echo "database get failed; mydb.json holds nothing" >&2
        exit 1
    fi
    domain=$(jq -r '.services[] | select(.service_type=="mcp") |
        .public_domain' mydb.json)
    token=$(jq -r '.services[] | select(.service_type=="mcp") |
        .mcp_config.init_tokens' mydb.json)
    if [ -z "$domain" ] || [ "$domain" = null ] ||
       [ -z "$token" ] || [ "$token" = null ]
    then
        echo "mydb.json carries no public MCP domain or token" >&2
        exit 1
    fi
    curl -sS --fail "https://$domain/mcp/v1" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/event-stream" \
        -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
             "params":{"protocolVersion":"2024-11-05",
             "capabilities":{},"clientInfo":{"name":"curl-example",
             "version":"1.0"}}}'

`--fail` is what makes this a readiness check, not a request. Without
`--fail`, `curl` exits 0 on the 503 a server that is still starting
returns, and a retry loop would stop on the first attempt. The
redirection truncates the file before the command runs, so a failed
read leaves an empty file that `jq` reads happily. A service carrying
no token yields the four characters `null`, which `curl` would send as
a bearer token of that name. Re-run the read instead of reusing the
file later. A file written for another database produces a successful
call against the wrong server, with nothing to notice.

## Waiting and service state

A services write moves the database to `modifying` for the duration
and settles it back to `available`. No BYOC response has a task
identifier, so `--wait` finds the task by subject. The CLI reads the
database's newest task before the write, and tracks the first one that
differs. `--follow` streams that task's step messages, instead of
requiring repeated status reads.

Without either flag, the command exits 0 the moment the API accepts
the change. In text output, the command prints the
`task list --subject-id <db-id>` call to monitor with. Under `-o json`
or `-o yaml`, the command prints nothing there, so a script builds
that call itself. Either way, that call followed by `task get` is how
a failed write explains itself, and the
[Tasks and async operations](../tasks-and-async.md) guide covers task
inspection in full.

A write returns the updated database, not a service object. In text
output that means a confirmation sentence on `stderr` and no table at
all. Under `-o json` or `-o yaml` the database object goes to stdout,
which is where a script reads back the new service id.

A resource reports its lifecycle in `status` and a service reports its
own in `state`. They are different field names with different value
sets, and a script reading one where the other lives finds nothing.
The [Output formats and paging](../output-and-paging.md) guide owns
both vocabularies.

`state` is not the deployment signal. The platform records `state`
when a write succeeds, and does not refresh it afterward. The value
therefore describes what the last successful write observed, not the
service now. Reading state repeatedly while waiting for `running`
can outlast a deploy that already succeeded. A failed deploy reports
on the task, not in `state`. A failed deploy can leave the database
`available`, with the entry's `state` empty or still carrying the
value an earlier write stored. As a result, wait on the task, and
treat a `state` of `failed`, where one appears, as naming a service
that did not come up. To know a deployed service is answering, ask it,
the way the MCP readiness check above does. The
[Tasks and async operations](../tasks-and-async.md) guide covers the
signals in full.

## Dry runs

Every write on this page takes `--dry-run`, which runs the
client-side checks, reports the request it would have sent, and stops.
The deploy-versus-update guard reports into that report, so a dry run
tells you which of the two the CLI thinks you are doing. Secret values
are masked in the preview. Nothing is submitted, so a clean dry run
means the listed checks passed, not that the API will accept the
request. The [Dry runs](../dry-run.md) guide covers the
limits.

## Removing a service

`service remove` takes the database and the type, not the service id.
Removal is irrecoverable, because the service's configuration and
credentials are discarded. The command prompts unless `--force` is
given. The other services on the database survive:

    pgedge starfleet byoc database service remove <db-id> mcp \
        --force --wait

The type argument is not checked against the three the CLI knows.
`remove` reads the database, sends back every service whose type does
not match the string you typed, and reports success. A misspelt type,
or a type that was never deployed, therefore removes nothing and still
exits 0. Confirm a removal with `service list`, not with the exit
code. Managed refuses both cases outright.

The prompt is decided by `stdin`. In a script, a CI job or an agent,
`stdin` is not a terminal. The command then fails with a usage
error asking for `--force`, instead of hanging. Redirecting the output
changes nothing.

## Next steps

- The [Manage BYOC databases](databases.md) guide covers the
  database these services attach to, and the built-in roles they
  connect as.
- The [Deploy managed services](../managed/services.md) guide covers
  the same services on a managed database. A managed database differs
  in enough places that this recipe does not carry across unchanged.
- The [Tasks and async operations](../tasks-and-async.md) guide covers
  waiting on a deploy and reading the task when one fails.
- The [pgedge starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
