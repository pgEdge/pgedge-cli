# Deploy managed services

A managed database can run two services beside Postgres, and this page
describes deploying, reaching and removing them. The MCP server gives
an LLM a query interface over the database. The RAG server answers
questions from retrieval pipelines built over the database's own
tables. PostgREST is a third service type in the API, but the platform
does not accept one on a managed database.

A service is addressed by type rather than by an identifier, because a
database carries at most one of each. The read commands and `remove`
live under `database service`, and the write commands live under
`database mcp` and `database rag`. A deployed service is closed to
every address until you allow one, and
[Allowing a client to reach a service](#allowing-a-client-to-reach-a-service)
says how.

## Before You Start

You need a tenant with a managed plan. You also need the full UUID
of a database that reports `available`, named `<database-id>` below.
`pgedge starfleet managed database list` shows the UUID, and a name
from that list is not an identifier. Every services write requires
that status, and a busy database refuses one.

## The commands

The following table lists the service commands.

| Command | What it does |
|---|---|
| `database service list <db>` | Lists the deployed services as SERVICE ID, TYPE, STATE and ENDPOINT. |
| `database service get <db> <type>` | Shows one service, named by type. |
| `database service remove <db> <type>` | Tears one down. Destructive, and prompts unless `--force` is given. |
| `database mcp deploy <db>` | Creates the MCP service. |
| `database mcp update <db>` | Reconfigures the deployed MCP service. |
| `database rag deploy <db>` | Creates the RAG service. |
| `database rag update <db>` | Reconfigures the deployed RAG service. |

Every write above is asynchronous and takes `--wait`, `--follow`,
`--wait-timeout` and `--wait-interval`. The reads take none of them.
An unknown type, or a malformed database identifier, is refused at
exit status 2 before any request leaves. The unknown-type refusal
names the three types the CLI knows.

Every write also takes `--dry-run`, which runs the client-side checks,
reports the request it would have sent, and stops. The
deploy-versus-update guard reports into it, so a dry run tells you
which of the two the CLI thinks you are doing. The
[Dry runs](../dry-run.md) page describes what a dry run checks and
does not check.

## Deploy versus update

`deploy` refuses to run when a service of that type is already
deployed, and `update` refuses when none is. Both checks are
client-side over a read the command has already made, so a refusal
sends no write. `deploy` exits at exit status 1, naming `update` as
the fix, and `update` exits at exit status 1, appending the `deploy`
command to run instead.

`deploy` is therefore not idempotent. A caller may want one command to
do either job. That caller branches on `service get`, which exits at
exit status 4 when the type is absent:

    pgedge starfleet managed database service get "$DB" mcp \
        >/dev/null 2>&1
    case $? in
      0) pgedge starfleet managed database mcp update "$DB" \
             --allow-writes ;;
      4) pgedge starfleet managed database mcp deploy "$DB" \
             --allow-writes ;;
      *) echo "service get failed; deploying nothing" >&2; exit 1 ;;
    esac

Branch on exit status 4 alone, never on "non-zero". A plain
`if pgedge ...; then update; else deploy; fi` reads an authentication
failure, a network error or a mistyped identifier as "absent". That
command then deploys a second service off a read that never ran.

`service remove` on an absent type also exits at exit status 4. Its
message and `update`'s open with the same words, so read the exit
code, not the message text.

## Configuring the MCP server

Every MCP flag is optional. A deploy with none of them set yields a
read-only server with an API-generated bearer token.

Every secret on this page is a plain string flag with no file or
`stdin` alternative, on both services: `--embedding-api-key`,
`--init-tokens`, `--init-users`, `--embedding-llm-api-key` and
`--completion-llm-api-key`. A value typed on the command line lands in
your shell history. That value also stays visible in `ps` to anyone on
the same host for as long as the command runs. Read each one from the
environment instead, as the examples below do. Export each one from
something that is not a shell history file.

The following table lists the MCP configuration flags and marks the
ones the API treats as secrets.

| Flag | Secret | Notes |
|---|---|---|
| `--allow-writes` | No | Grants the LLM insert, update and delete access through the query tool. Off by default. |
| `--embedding-provider` | No | Accepts `openai` or `voyage`. Any other value is exit status 2 before any request. |
| `--embedding-model` | No | The API requires it alongside the provider. The CLI does not check for it. |
| `--embedding-api-key` | Yes | Required whenever `--embedding-provider` is passed. With no key passed or already stored, `deploy` and `update` both fail at exit status 2 before the change is sent. On `update`, omit the flag to reuse the stored key. |
| `--init-tokens` | Yes | Bearer token forwarded to the server as INIT_TOKENS. The API generates one when the flag is omitted. |
| `--init-users` | Yes | Comma-separated `username:password` pairs forwarded as INIT_USERS. |

`update` changes only the flags you pass, and reads everything else
back from the deployed service. `--allow-writes` is a boolean, so turn
write access on by passing it and off with `--allow-writes=false`.
Omitting the flag leaves the current access level alone, so an
unrelated change cannot quietly revoke writes.

Deploying an MCP server with the embedding tool enabled takes the
provider triple together:

    pgedge starfleet managed database mcp deploy <database-id> \
        --embedding-provider openai \
        --embedding-model text-embedding-3-small \
        --embedding-api-key "$OPENAI_API_KEY" \
        --wait

## Configuring the RAG server

`rag deploy` requires seven flags: both LLM triples and a pipeline
file. The API rejects a partial configuration outright, so the CLI
refuses one first.

The following table lists the RAG configuration flags and marks the
ones the API treats as secrets.

| Flag | Secret | Notes |
|---|---|---|
| `--embedding-llm-provider` | No | Required on deploy. |
| `--embedding-llm-model` | No | Required on deploy. |
| `--embedding-llm-api-key` | Yes | Required on deploy. The stored key is reused when the flag is omitted on update. `rag deploy` requires every field together, so a key is already stored before any update runs. |
| `--completion-llm-provider` | No | Required on deploy. |
| `--completion-llm-model` | No | Required on deploy. |
| `--completion-llm-api-key` | Yes | Required on deploy. The stored key is reused when the flag is omitted on update. `rag deploy` requires every field together, so a key is already stored before any update runs. |
| `--pipeline-config` | No | Path to a JSON file holding the pipeline definitions. Required on deploy. |
| `--top-n` | No | Default number of results retrieved per pipeline. A zero counts as an explicit zero. |
| `--token-budget` | No | Default maximum completion tokens across all pipelines. A zero counts as an explicit zero. |

The CLI checks neither provider name. A value the platform does not
know comes back as an API error, not as exit status 2. The API's enum
for a RAG LLM provider is `openai` and `anthropic`. The CLI's own
`--embedding-llm-provider --help` text offers a different example
pair, `openai` and `voyage`. Take the accepted values from the API,
not from that help text.

`--pipeline-config` takes a file holding either a bare array of
pipelines or an object with a `pipelines` key. The second shape is the
one the API returns. A configuration read back from `database get` can
therefore be written to a file and passed straight back.

A minimal pipeline file names one pipeline over one table:

    {
      "pipelines": [
        {
          "name": "support",
          "tables": [
            {
              "table": "public.documents",
              "text_column": "body",
              "vector_column": "embedding"
            }
          ]
        }
      ]
    }

The CLI validates that file before sending anything. The checks
require at least one pipeline, a name on each, no duplicate names, and
`_default` refused as reserved. The file also needs at least one table
per pipeline, with `table`, `text_column` and `vector_column` set on
every table. Each is exit status 2, the code an unreadable path or
unparsable JSON also gets, and no write goes out. Neither the CLI nor
the API checks that the tables exist.

`system_prompt`, `top_n`, `token_budget`, `min_similarity`,
`hybrid_enabled` and `vector_weight` tune retrieval per pipeline, and
each table entry also accepts `filter` and `id_column`.
[The managed OpenAPI spec](https://api.pgedge.com/managed/v1/openapi.json)
has the shapes and defaults.

A RAG deploy reads its keys from the environment:

    pgedge starfleet managed database rag deploy <database-id> \
        --embedding-llm-provider openai \
        --embedding-llm-model text-embedding-3-small \
        --embedding-llm-api-key "$OPENAI_API_KEY" \
        --completion-llm-provider anthropic \
        --completion-llm-model claude-sonnet-5 \
        --completion-llm-api-key "$ANTHROPIC_API_KEY" \
        --pipeline-config pipelines.json \
        --wait

`rag update` changes only the flags you pass. `--pipeline-config` is
the complete pipeline list the CLI sends, and a pipeline absent from
the file is dropped. Include every pipeline you want to keep. Pass an
API key only to rotate one, because the API never returns a RAG key.
The CLI therefore cannot read one back, and the API refills an omitted
key from stored state.

The API's RAG schema carries a CORS block the CLI binds no flag to. A
browser client calling a pipeline directly therefore has nothing to
set here.

### Who can reach a RAG pipeline

A deployed pipeline answers requests that carry no Authorization
header. A plain `curl` with no credential answers 200, quoting the
database's own rows. The MCP service at the same hostname answers 401
to the same request instead. Beyond the RAG endpoint's own allowlist,
the only barrier is the randomly generated hostname. Pipeline names
are enumerable, because an unknown name answers 404 where a real one
answers 200. Every request spends the tenant's own embedding and
completion credits, on the keys supplied at deploy time. Point a
pipeline only at data you would publish to every address you allow.

## Allowing a client to reach a service

Each service has its own allowlist: the IPv4 addresses and blocks that
may open a connection to its endpoint. That list is independent of the
Postgres endpoint's list and of every other service's. Allowing an
address on Postgres does not allow it on MCP. A service starts closed
when it is deployed, and nothing reaches its endpoint until you add a
rule. A service you have deployed can refuse a client. Add the
client's address to that service's list:

    pgedge starfleet managed database allowlist add <database-id> \
        <cidr> --service mcp --wait

`<cidr>` is a bare IPv4 address, which the API stores as a `/32`, or a
CIDR block. The CLI sends it as typed, so a malformed or IPv6 value
comes back as an API error, not as exit status 2. The write needs the
database `available`, moves it through `modifying`, and takes the same
wait flags as the writes above.

`--service` takes `mcp`, `rag` or `postgrest`, and a type not deployed
on the database is exit status 4 with nothing sent. Without
`--service` the command edits the Postgres endpoint's list instead.
`mcp update` and `rag update` leave the service's rules unchanged.
Removing a service also removes its rules. `--my-ip` in place of
`<cidr>` adds the address the API sees this command arrive from. That
address may differ from the address your client connects from. The
other `allowlist` commands are on
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md).

## Where the secrets show

MCP's secrets come back from a read, and RAG's never do. The following
table lists where each one appears.

| Field | Service | Returned by |
|---|---|---|
| `mcp_config.init_tokens` | MCP | The single-database GET, so `database get`, `service get` or `service list` under `-o json`. |
| `mcp_config.init_users` | MCP | As above. |
| `mcp_config.embedding_api_key` | MCP | As above. |
| The RAG LLM API keys | RAG | Nothing. Both are write-only. |

`database list` omits them, so a configuration built from a list entry
arrives with its secrets blank. That blank looks the same as a service
that has none set. Read a single database before building a
configuration from one. No table view prints a secret in any format,
so a working MCP call needs `-o json`.

## The endpoint to dial

Both services answer outside the CLI, at the URL the API reports in
the service's `uri`. `service list`, `service get` and `database get`
all print that value as ENDPOINT, and `-o json` carries the field
itself. Ask the API for it instead of assembling it, because the path
segment is the platform's to choose. PostgREST, for instance, routes
under `rest`, not under its own type name.

A reported `uri` carries the service's version segment, so it reads
`https://<domain>/mcp/v1` or `https://<domain>/rag/v1`. The URL has no
port, because a managed service is always reached over HTTPS on 443.
The field is nullable, and the API omits it until the database's
domain is assigned. The CLI then composes a fallback from the
database's domain and the service's path segment, with no version
segment. ENDPOINT is empty when neither value is available.

MCP speaks streamable-HTTP at the `uri` itself, so a client posts to
`<uri>` with the bearer token from `mcp_config.init_tokens`. A RAG
pipeline answers one segment below, at `POST <uri>/pipelines/<name>`,
where `<name>` is the pipeline's name from the configuration file, and
it answers without a credential.

## Verify a deployed MCP server

Exit status 0 on a deploy means the API accepted the write, not that
the server answers. The endpoint returns 503 while it starts, and an
`initialize` call that succeeds is the only readiness signal. The
transport wants an `Accept` header naming both JSON and the event
stream. The first request on a connection must be `initialize`,
because a cold `tools/list` is a lifecycle violation.
The server may answer with an `Mcp-Session-Id` header, which later
requests on the same session echo back.

This reads the endpoint and the token, stops if either read fails,
then opens a session:

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
    curl -sS --fail "$uri" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/event-stream" \
        -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
             "params":{"protocolVersion":"2024-11-05",
             "capabilities":{},"clientInfo":{"name":"curl-example",
             "version":"1.0"}}}'

`--fail` makes this a readiness check. Without `--fail`, `curl` exits
0 on the 503 a starting server returns, and a retry loop would stop on
the first attempt. The redirection truncates the file before the
command runs, so a failed read leaves an empty file that `jq` reads
happily. A service with no token yields the four characters `null`
instead. `curl` would send that string as the bearer token. Re-run the
read instead of reusing the file later. A file written for another
database produces a successful call against the wrong server, with
nothing to notice. `notifications/initialized` and `tools/list` follow
on the same session when `initialize` succeeds.

## MCP query limits

The query tool takes a `limit`, defaulting to 100 and topping out at
1000, and an `offset` to continue from. The server appends both to a
query that does not already carry them. A truncated response says more
is available and names the offset to continue from. A complete result
of the same size reports only its row count.

The check for an existing limit is a substring test over the query
text. Any occurrence of `LIMIT`, including a column name or alias such
as `unlimited`, stops the server appending anything, and silences the
truncation notice too. Pass `limit` as a parameter rather than writing
the clause into the SQL.

The query tool is named `query_database`, and a `tools/call` request
sends the SQL text and the limit as separate arguments.
<!-- doc-gate: deliberately-wrong query_database — an MCP tool name
from openapi/managed.yaml, not a CLI struct field -->
This call reuses the `uri` and `token` read in the readiness check
above:

    curl -sS "$uri" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/event-stream" \
        -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
             "params":{"name":"query_database","arguments":{
             "query":"SELECT * FROM public.documents","limit":50}}}'

## Waiting and service state

A services write moves the database to `modifying` for the duration
and settles it back to `available`, or to `degraded` when the work
fails. A write that fails outright can leave the row at `modifying`
without settling, and every other write is refused while the row sits
there.

Each write spawns a task named `update-managed`, and none of them
returns that task's identifier. `--wait` therefore finds the task by
subject. The CLI records the database's newest task before the write,
and tracks the first one that differs. Without `--wait` the command exits at
exit status 0 the moment the API accepts the change. In text output, the
command then prints the `task list --subject-id <database-id>` call to
monitor with. Under `-o json` or `-o yaml` the command prints nothing
there, so a script has to build that call itself. Either way, that
call followed by `task get` is how a failed write explains itself. The
[Tasks and async operations](../tasks-and-async.md) page describes the
wait flags and task inspection.

Under `-o json` or `-o yaml` a services write prints the updated
database on stdout. A script can read back the new service id and
assigned port from that output sooner than from any other read.

A task reaching `succeeded` means the API-side work finished, not that
the running server reflects it, for an `mcp deploy`, an `mcp update`
and a `rag update` alike. A script that writes and immediately queries
should tolerate briefly seeing the old configuration, or a 503 from
the endpoint. A `rag deploy` has no readiness handshake to measure
against, so its readiness is tested the same way: call the pipeline
endpoint.

The service `state` field reads `running` as soon as the write
completes, while the server is still answering 503. Repeated reads of
`state` therefore say nothing about whether the service answers. A
service `state` and its database `status` can also disagree. Act on
`failed`, and read `running` as a report that a write finished, not as
permission to dial.

## Removing a service

`service remove` takes the database and the type. Removal is
destructive, because the service's configuration and credentials are
unrecoverable, so the command prompts before it acts. The other
service types on the database survive:

    pgedge starfleet managed database service remove <database-id> mcp

`--force` skips the prompt. The prompt comes before the existence
check, so removing a type that is not deployed asks first and then
exits at exit status 4. The endpoint keeps answering for a few seconds
after the command returns. An immediate check can therefore still
reach a service on its way out.

## PostgREST on managed

The `postgrest` command group exists and its flags are documented,
but the platform rejects the `postgrest` service type outright. A bare
`postgrest deploy` is exit status 2 from its own required flags,
`--db-schemas` and `--db-anon-role`. Supply those and the platform
answers 400.

`postgrest update` does reach the platform. The command resolves
credentials and reads the database before it checks whether a
PostgREST service exists. The command therefore needs a working
profile to fail the way it does: with no credentials it exits at exit status 5,
not exit status 1. When the read succeeds, the client-side guard exits at exit
status 1 and names `deploy`, because no PostgREST service can exist for it to
update.

A bad flag value is the exception to that ordering. `--db-pool`,
`--max-rows` and `--jwt-secret` are range-checked before the client is
built. `postgrest update --db-pool 0` is therefore exit status 2 with
no credentials configured, and never reaches the profile or the guard.

## Next steps

- The [Output formats and paging](../output-and-paging.md) page owns
  the `.status` and `.state` vocabularies.
- The [Exit codes](../exit-codes.md) page explains what exit status 4 does
  and does not tell you about a missing service.
- The [CI and automation](../ci.md) page describes running these
  writes unattended, including bounding a wait.
- The [pgedge starfleet managed command reference](../reference/starfleet-managed.md)
  lists every flag.
