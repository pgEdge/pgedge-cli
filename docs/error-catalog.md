# Error catalog

A failing command prints one line to `stderr`, and that line is the
fastest way into the documentation. Find the message here, read what it
means, then follow the link to the guide that covers the behavior
behind it. The [exit codes guide](exit-codes.md) carries the contract
behind the numbers, and the
[troubleshooting guide](troubleshooting.md) covers the same ground
starting from the exit code instead of the text.

Two conventions run through the tables below. A value the CLI
substitutes at run time appears in angle brackets, so
`unknown profile "<name>"` reaches you as `unknown profile "prod"`. A
long message is quoted by its opening clause, which is enough to
recognize it by.

This catalog covers the messages a caller meets often. It is not
exhaustive, and a message that is missing from it is still governed by
the exit-code contract.

## Authentication and credentials

Credential resolution runs before any request leaves, and the token
exchange runs on the first call that needs one. The following table
describes the messages both steps print:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `no credentials found — use --client-id/--client-secret, or run 'pgedge starfleet auth login'` | 5 | No profile carries a client ID and secret, and neither flag was passed. Log in, or supply both flags. | [Authentication and profiles](auth-and-profiles.md) |
| `--client-id given without --client-secret: supply both or neither` | 2 | Half a credential pair reached the command. Pass both flags, or drop both and let the profile answer. | [Authentication and profiles](auth-and-profiles.md) |
| `authentication failed: <reason>` | 5 | The token exchange did not complete. The reason names the cause, most often a refused credential or a token endpoint that could not be reached. | [Authentication and profiles](auth-and-profiles.md) |
| `authentication error (401): <body>` | 5 | The API rejected the bearer token. Run `pgedge starfleet auth logout` and log in again to rebuild the cache. | [Authentication and profiles](auth-and-profiles.md) |
| `authentication error (403): <body>` | 5 | The credential authenticates and the tenant is not permitted to do this. Confirm which tenant is active before changing the credential. | [Health checks](doctor.md) |
| `this tenant's plan does not allow this resource: the active credential authenticates fine, but its tenant's plan doesn't include this capability.` | 5 | Plan entitlement rather than a bad credential, so no login fixes it. Stop any retry loop and ask about the tenant's plan. | [Exit codes](exit-codes.md) |
| `<command> needs a signed-in user. The CLI authenticates with a client ID and secret, which identifies an application rather than a person` | 5 | Team invites need a person behind the token, which a client credential never supplies. Invite from the Team page in the pgEdge Starfleet UI, as the message says. | [Team onboarding](starfleet/team-onboarding.md) |
| `client created but the API returned no body — the client secret is unrecoverable; delete the client and retry` | 1 | The API client exists and its secret was never printed. Delete the client and create it again. | [Account, tenant and API clients](starfleet/account-and-clients.md) |

## Profiles and the config file

The config file resolves on every invocation, including commands that
never mention it. The following table describes what each failure in
that path prints:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `unknown profile "<name>" — configured profiles: <list>` | 1 | The name is well formed and not configured. Pick one of the names the message lists. | [Authentication and profiles](auth-and-profiles.md) |
| `unknown profile "<name>" — no profiles are configured; run 'pgedge starfleet auth login --profile <name>' or 'pgedge controlplane config set --base-url <url> --profile <name>' to create one` | 1 | The config file holds no profiles at all. Either command in the message creates the first one. | [Authentication and profiles](auth-and-profiles.md) |
| `--profile given an empty value: name a profile, or omit the flag to use the active one` | 2 | The flag token arrived with nothing after it, which is a malformed command line rather than a missing profile. | [Configuration and environment](configuration.md) |
| `--config given an empty value: name a file, or omit the flag to use the default` | 2 | Same shape as the previous row, and the same fix. | [Configuration and environment](configuration.md) |
| `config: <path> does not exist (check the path, create the file and any parent directory, or omit --config to use the default)` | 1 | An explicitly named config file is absent. The default path is allowed to be absent and this one is not. | [Configuration and environment](configuration.md) |
| `config: parse <path>: <reason>` | 1 | The file is present and not valid YAML. Every command fails this way until the file parses. | [Configuration and environment](configuration.md) |
| `profile "<name>": timeout "<value>" is not a duration — use a Go duration such as 30s, 5m or 1h, remove the field to take the 30s default, or pass --timeout to override it for one command` | 2 | A Control Plane profile carries an unparseable timeout. Fix the field, or override it per command. | [Configuration and environment](configuration.md) |

## Command syntax

The command line is checked before a command runs, and several commands add
a check of their own. The following table describes the refusals that
come out of that stage:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `unknown command "<name>" for "<parent>"` | 2 | The word is not a command of that group, and a near miss is offered as a correction. Appending `--help` does not change the outcome, which makes it a reliable test of whether a command exists. | [Exit codes](exit-codes.md) |
| `unsupported output format: "<value>" (want text, json, yaml)` | 2 | `--output` takes those three values only. | [Output formats and paging](output-and-paging.md) |
| `unknown module "<name>" — this binary carries: controlplane, starfleet` | 2 | `pgedge llms` was given a module this binary does not embed a reference for. | [AI agents](ai-agents.md) |
| `unknown help topic "<topic>" — run 'pgedge help' for the command list` | 2 | `pgedge help` was given words that name no command. | [Exit codes](exit-codes.md) |
| `nothing to update — pass --name` | 2 | An update command was given no change to make, so nothing was sent to find that out. | [Account, tenant and API clients](starfleet/account-and-clients.md) |
| `nothing to update — pass --name and/or --description` | 2 | The same refusal on `starfleet client update`, which has two updatable fields. | [Account, tenant and API clients](starfleet/account-and-clients.md) |
| `--database and --host are mutually exclusive` | 2 | A Control Plane task belongs to one entity. Name one or the other. | [Tasks and async operations](tasks-and-async.md) |
| `provide --database or --host: a task belongs to the database or host it ran against, and 'pgedge controlplane task list' shows that value in its ENTITY column` | 2 | A Control Plane task ID alone does not locate the task. The listing names the entity to pass. | [Tasks and async operations](tasks-and-async.md) |
| `this operation is destructive; run with --force to confirm, or run interactively to be prompted` | 2 | A destructive command ran without a terminal to prompt on. Pass `--force` in a script. | [CI and automation](ci.md) |
| `aborted: not confirmed` | 2 | The prompt got an answer other than `y`, so nothing was sent. | [CI and automation](ci.md) |

## Identifiers and missing resources

Resource identifiers are full UUIDs apart from a handful of named
exceptions, and a malformed one is refused locally while a well-formed
one that names nothing is refused by the server. The following table
describes both halves:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `invalid <kind> "<value>": <reason>` | 2 | A UUID argument did not parse, so no request left. A name from a `list` command's NAME column is not an ID. | [Exit codes](exit-codes.md) |
| `invalid size id "<value>": sizes are addressed by UUID, not by name` | 2 | The size catalog prints names and is addressed by ID. The rest of the message names the command that prints the IDs. | [Provision a managed database](managed/provision.md) |
| `resource not found (404): <body>` | 4 | The server's handler says the resource does not exist. Verify the ID with the matching `list`. | [Exit codes](exit-codes.md) |
| `database <id> not found` | 4 | The server answered success, but the response body did not describe a database. A database that genuinely does not exist prints `resource not found (404)` above instead; run `database get <id>` to check the database directly. | [Exit codes](exit-codes.md) |
| `task "<id>" not found` | 4 | The task ID names nothing the tenant can see. `task list` shows what it can. | [Tasks and async operations](tasks-and-async.md) |
| `no "<type>" service deployed on database <id>` | 4 | The database exists and carries no service of that type. | [Deploy managed services](managed/services.md) |
| `no node named "<name>" in cluster <id> — valid names: <list>` | 4 | A node name was not one of the cluster's. The message lists the names that are. | [Manage BYOC clusters](byoc/clusters.md) |
| `node <name> of database <id> has no host; it has internal_host <value>, reachable from inside the cluster's network with --internal` | 1 | A private BYOC cluster publishes only the internal address. Pass `--internal` to get it. | [Connect an application](byoc/connect-an-application.md) |
| `database <id> has no connection host yet; it is still being created, or its status is not available` | 1 | The database is not far enough along to have an address. Wait for it to reach `available`. | [Managed](managed/connect-an-application.md), [BYOC](byoc/connect-an-application.md) |

## Flag values refused before the request

A value outside the bounds a flag documents is spelled correctly and is
still refused locally. The following table describes the checks that
fire most often:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `--user-type given an empty value: name a role (admin, app or app_read_only), or omit the flag to use app` | 2 | The flag arrived with nothing after it. | [Connect an application](managed/connect-an-application.md) |
| `unknown user type "<value>" (expected one of: admin, app, app_read_only)` | 2 | The value is not one of the three roles a managed connection string is built for. | [Connect an application](managed/connect-an-application.md) |
| `unknown role "<value>" (expected one of: admin, app, app_read_only)` | 2 | `--role` takes those three canonical spellings, plus their long-form aliases. | [Rotate a managed password](managed/rotate-credentials.md), [Rotate a BYOC password](byoc/rotate-credentials.md) |
| `invalid --interval value "<value>": expected value,unit — digits, then second, minute, hour, day, week, month or year (e.g. 15,minutes)` | 2 | The byoc metrics lookback window takes a comma-separated pair. The check runs before the request. | [BYOC logs and metrics](byoc/logs-and-metrics.md) |
| `invalid --window value "<value>": expected value,unit — up to four digits, then second, minute, hour or day (e.g. 15,minutes)` | 2 | The managed metrics window is the same shape with a narrower unit set. | [Managed logs and metrics](managed/logs-and-metrics.md) |
| `invalid --max-lines value <n>: expected <min> to <max>` | 2 | The managed log read is bounded at both ends. | [Managed logs and metrics](managed/logs-and-metrics.md) |
| `--db-pool must be between <min> and <max>, got <n>` | 2 | A PostgREST setting outside the contract's bounds. | [Deploy managed services](managed/services.md) |
| `--max-rows must be between <min> and <max>, got <n>` | 2 | The same class of refusal on the row ceiling. | [Deploy managed services](managed/services.md) |
| `--jwt-secret must be at least <n> characters, got <n>` | 2 | The secret is too short for the service to accept it. | [Deploy managed services](managed/services.md) |
| `unknown Postgres version "<value>" (expected one of: 18, 17, 16); see 'pgedge starfleet managed pg-version list'` | 2 | `--pg-version` takes a major version the catalog publishes. | [Provision a managed database](managed/provision.md) |
| `database name "<name>" is invalid: must be lowercase letters and digits only, starting with a letter (no hyphens, underscores, or other characters)` | 2 | A managed database name is narrower than a BYOC one, which does accept underscores. | [Provision a managed database](managed/provision.md) |
| `--region is required: the API publishes <n> regions (<list>). A region is fixed for the life of the database, so the CLI will not choose one for you` | 2 | More than one region is available and the choice cannot be undone later, so name one. | [Provision a managed database](managed/provision.md) |
| `unknown region "<value>" (expected one of: <list>)` | 2 | The region is not one the tenant's catalog publishes. | [Provision a managed database](managed/provision.md) |
| `--nodes must be at least 1` | 2 | `controlplane database init` writes a template with at least one node in it. | [Manage Control Plane databases](controlplane/databases.md) |
| `JSON output requires --interactive (-i); non-interactive init emits YAML only.` | 2 | `init` emits JSON only from the interactive interview. Drop `-o json` for the YAML template. | [Manage Control Plane databases](controlplane/databases.md) |
| `--display-name is <n> characters; the limit is <n>` | 2 | The display name is over the length the API accepts. | [Account, tenant and API clients](starfleet/account-and-clients.md) |

## Spec files and input documents

An input document counts as part of the command, so a file that cannot
be read, parsed or trusted is exit 2 and no write is sent. The
following table describes the refusals a spec file produces:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `read spec file: <reason>` | 2 | The path named by `-f` is absent or unreadable. | [Manage Control Plane databases](controlplane/databases.md) |
| `parse spec file: <reason>` | 2 | The file is not valid YAML. | [Manage Control Plane databases](controlplane/databases.md) |
| `parse spec file: unrecognized field "<name>". Check the spelling against the template that generated this spec: 'database init' for a database spec, 'database restore template' for a restore spec` | 2 | A key matches nothing in the schema, which usually means a typo or the wrong template. | [Manage Control Plane databases](controlplane/databases.md) |
| `<field> is still "CHANGE-ME"; <remedy>` | 2 | A template placeholder reached a write unedited. The message names the first field still carrying one. | [Stand up a local Control Plane](controlplane/local-server.md) |
| `a restore spec file is required: -f <path> (or - for stdin)` | 2 | `controlplane database restore` reads its request from a document. | [Back up and restore (Control Plane)](controlplane/backup-restore.md) |
| `read pipeline config "<path>": <reason>` | 2 | The RAG pipeline document could not be read. The read happens after the database lookup, so the ID was already good. | [Deploy managed services](managed/services.md) |
| `<label> "<value>" is not in major.minor format (e.g. "16.14"); set a full Postgres version` | 2 | A Control Plane spec pins a major version where the schema wants both parts. | [Manage Control Plane databases](controlplane/databases.md) |

## Transport and server errors

A server that could not be reached is exit 1, and so is a non-2xx the
CLI has no sharper reading for. The following table describes the
transport and server messages:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `<action>: <reason>` followed by `(is the control-plane reachable at the configured --base-url? for mTLS check --ca-cert/--client-cert or use --insecure for a dev server)` | 1 | The Control Plane did not answer. The hint names the three things that usually explain it. | [Stand up a local Control Plane](controlplane/local-server.md) |
| `no control-plane server responded to /v1/version; tried: <urls>` | 1 | A profile listing several base URLs probed each in turn and none answered. A 401 or 403 during that probe reads as one more candidate that did not answer. | [High availability operations](controlplane/ha.md) |
| `this server does not serve an endpoint this command needs` | 1 | A router, proxy or gateway answered instead of the API. The base URL is probably not an API root. On the Control Plane the message adds that the server may be older than the API this CLI was built against. | [Troubleshooting](troubleshooting.md) |
| `this Control Plane has no cluster yet — run 'pgedge controlplane cluster init' to create one, or 'pgedge controlplane cluster join' to join an existing cluster.` | 1 | Every command that reads or writes cluster state answers this way until a cluster exists. | [Stand up a local Control Plane](controlplane/local-server.md) |
| `the resource is busy with another operation and this one needs it idle. Wait for it to settle and retry` | 1 | A managed write hit a resource mid-operation. This one is worth retrying, which the exit code alone does not say. | [Tasks and async operations](tasks-and-async.md) |
| `a branch of this database refuses the request, and waiting will not clear it` | 1 | A `branch create` at the database's branch limit, or a `database delete` or `database resize` while branches exist. Retrying changes nothing: delete a branch first, or pass `--delete-branches` to `database delete`. | [Provision a managed database](managed/provision.md) |
| `API error (<status>): <body>` | 1 | The catch-all for a non-2xx with no sharper reading. Read the quoted body, which is what the server said. | [Exit codes](exit-codes.md) |
| `database in unmodifiable state: modifying` | 1 | Server text, arriving inside the row above. A database accepts one change at a time, so wait for `available` between service operations. | [Deploy BYOC services](byoc/services.md) |
| `500 failed to read metrics` | 1 | Server text for a metrics parameter it could not read. A malformed `--interval` is refused locally before it can produce this. | [BYOC logs and metrics](byoc/logs-and-metrics.md) |

## Deadlines and tasks

Exit 3 is a deadline expiring, and a task that reached a terminal
failure is exit 1. The following table describes both, and neither
means the command is safe to repeat blind:

| Message | Exit | What it means and what to do | Guide |
|---|---|---|---|
| `request timed out (<status>): <body>` | 3 | The server answered 408 or 504. | [Exit codes](exit-codes.md) |
| `timed out after <n>s waiting for task <id>` | 3 | A `--wait` ran out of `--wait-timeout`. The operation is probably still running, so read the task rather than retrying the write. | [Tasks and async operations](tasks-and-async.md) |
| `timed out after <n>s waiting for task <id> (last status: <status>)` | 3 | The same bound expiring, with the last status the poll saw. | [Tasks and async operations](tasks-and-async.md) |
| `timed out after <n>s waiting for a task on <id>` | 3 | The write was accepted and no task appeared for it inside the bound. | [Tasks and async operations](tasks-and-async.md) |
| `timed out after <d> reading task <id>'s log; --follow bounds each log poll itself, independently of --timeout` | 3 | A Control Plane `--follow` poll hit its own fixed bound. The follow as a whole has none. | [Tasks and async operations](tasks-and-async.md) |
| `<action>: <reason>` followed by `(the request timed out before the control-plane answered, so it may be busy rather than unreachable)`, then the note that --timeout bounds each request and --timeout 0 disables the bound. | 3 | A Control Plane request outran `--timeout`. A first `database create` on a host still pulling the Postgres image is the usual way to hit it. | [Manage Control Plane databases](controlplane/databases.md) |
| `task <id> failed` | 1 | The task reached a terminal failure. `task logs` prints the step trace that says why. | [Tasks and async operations](tasks-and-async.md) |
| `task <id> was canceled` | 1 | Someone canceled the task, or the server did. | [Tasks and async operations](tasks-and-async.md) |
| `list tasks: HTTP <status> carried no readable task list` | 1 | The task listing answered with a body the CLI could not read, so the wait could not start. | [Tasks and async operations](tasks-and-async.md) |

## Getting support

[Versions, uninstall and support](support-versioning-and-uninstall.md)
says where to take a problem, with the CLI or with a pgEdge product,
and what to attach so it can be acted on.
