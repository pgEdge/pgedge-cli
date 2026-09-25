# Running the CLI in CI and Automation

You can run the CLI unattended, in a CI pipeline, a container or a
scheduled job. An unattended run needs three things: credentials
supplied without a prompt, confirmations skipped, and scripts that read
the output and the exit status. The CLI designs both to be scripted
against.

The page leans on these terms:

- The credential pair is `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET`,
  set as environment variables.
- The exit status is the number a command returns to the shell.
- A bound is a time limit the CLI puts on a request or a wait.

## Supplying Credentials

Set `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` from your secret
store. The CLI treats the pair as a profile that lasts one invocation.
Each run authenticates from the secrets alone and leaves the runner's
disk as it found it. Supply the pair as follows:

- CI: map the two secrets into the environment of each step that runs
  `pgedge`.
- Docker and Kubernetes: pass both with `docker run --env`, or expose
  a Kubernetes secret to the container as environment variables.

The pair calls the production API unless a command passes
`--api-url`. Leave `--profile` off: alongside the pair, it is a usage
error with exit status 2.
[Authentication and profiles](auth-and-profiles.md) covers the rest of
the pair's rules.

A config file suits a host that needs several profiles. Write it to
`~/.pgedge/cli/config.yaml`, or mount it at
`/root/.pgedge/cli/config.yaml` in a container. The repository's
`examples/config.yaml` is the commented template.

Per-invocation flags (`--client-id`, `--client-secret`, `--api-url`)
also work. On a shared host, though, `ps` shows every argument list,
so prefer the environment variables or the file.
[Configuration and environment](configuration.md) covers the file's
layout. It also covers `HOME` and `XDG_CONFIG_HOME`, the two variables
a container run cares about.

Pass `--profile` and `--config` a value, or leave them off entirely.
Both refuse an empty string with exit status 2. As a result,
`--profile "$P"` with `$P` unset fails the step. It fails before the
CLI can silently resolve `current_profile` and run against a different
tenant.

## Skipping Prompts Deliberately

Destructive commands prompt for confirmation. To skip the prompt in
automation, pass `--force`. The flag goes on each command, so a script
names its own irreversible steps. With no terminal to prompt on, a
destructive command run without `--force` exits at once with exit
status 2.

## Reading the Exit Status Instead of Stdout

The exit status contract is one number, one meaning, across every
module. [Exit codes](exit-codes.md) carries the table and the
deliberate exceptions. These are the ones that matter for scripts:

- A success carrying no response body prints nothing to stdout, in
  text, json and yaml alike. The acknowledgment is exit status 0 and a
  sentence on stderr. Eighteen commands behave this way. Most are
  deletes. The rest are both modules' `rotate-password`,
  `managed database resize`, `byoc backup create`,
  `byoc database restore`, `byoc ingress service deregister` and
  `controlplane cluster join`.
  The rule follows the response, whatever the command's name:
  `controlplane database delete`, `controlplane host remove` and both
  modules' `database service remove` do print an object. Branch on
  the exit status. [Output formats and paging](output-and-paging.md)
  gives the full picture.
- `pgedge starfleet doctor` and `pgedge controlplane doctor` return
  exit status 0 even when the thing they diagnose is broken. Their job
  is to report, so a script reads their `-o json` fields to find a
  fault.
- A plan-entitlement rejection is exit status 5, the same class as a
  bad credential. An auth-retry loop should stop on it.

Diagnostics stay out of your pipes. `--verbose` and `--debug` write to
stderr, so `-o json | jq` stays clean with either enabled.

The straightforward pattern looks like this:

    set -e
    pgedge starfleet managed database rotate-password "$DB_ID" \
        --role app --force --wait

A failure still exits non-zero, so an empty stdout at exit status 0
means the operation succeeded.

## Bounding Your Waits and Knowing What Timed Out

`--timeout` bounds a single request in every module. It takes a Go
duration, defaults to 30s, and 0 disables it. The bound on a wait is a
different flag. Commands that take `--wait` check again every
`--wait-interval` and give up at `--wait-timeout`, both in seconds. In
byoc and managed, the same bounds cover `--follow`.

controlplane's `--follow` keeps running past `--wait-timeout`, with no
overall bound. Instead, each of its log requests gets its own fixed 30
seconds, independent of `--timeout`. As a result, `--timeout 0` still
stops a log request at 30 seconds. Only a `--timeout` below 30
seconds shortens one.

[Tasks and async operations](tasks-and-async.md) covers which commands
take those flags. It also covers which signal to trust for each kind
of write, and how to read a task when a wait ends badly.

A timeout is exit status 3 whatever produced it, with one exception. A
hung token exchange is exit status 5, because authentication is the
call that failed. That holds even when a deadline is what failed it.
Read stderr for which bound fired.

After exit status 3, check what happened before repeating a command. A
read is safe to repeat. A create or delete may already have reached
the server before the bound fired. Repeating it can then apply the
change twice.

## Treating a Dry Run as a Preflight

Every command that writes to an API takes `--dry-run`. It runs the
CLI's client-side checks and reports the request it would have sent.
A clean dry run means those checks passed. The API sees the request
only on the real run, so gate the pipeline on the real run's exit
status. Read the report's list of checks to see what it covered.
[Dry runs](dry-run.md) covers what each command checks and which
commands lack the flag. It also explains why the report goes to
stdout, even on commands that otherwise print nothing.

## Building a Complete GitHub Actions Pipeline

The following workflow takes a nightly backup of one managed database,
start to finish. It installs the CLI, takes the backup, and checks the
backup record until it reaches a terminal status. It checks the record
for two reasons. `backup create` has no `--wait`, and the task that
the create spawns reaches `succeeded` while the backup record is still
`pending`.
[Backing up and Restoring a pgEdge Starfleet Managed Database](managed/backup-restore.md)
explains why the record is the signal. The complete workflow:

    name: Nightly managed backup

    on:
      schedule:
        - cron: "0 2 * * *"
      workflow_dispatch:

    jobs:
      backup:
        runs-on: ubuntu-latest
        env:
          DB_ID: ${{ vars.PGEDGE_DATABASE_ID }}
        steps:
          - uses: pgEdge/pgedge-cli@v0.5.0-beta.3

          - name: Take the backup
            env:
              PGEDGE_CLIENT_ID: ${{ secrets.PGEDGE_CLIENT_ID }}
              PGEDGE_CLIENT_SECRET: ${{ secrets.PGEDGE_CLIENT_SECRET }}
            run: |
              set -euo pipefail
              pgedge starfleet managed backup create \
                  --database-id "$DB_ID" --kind hot \
                  --timeout 60s -o json > backup.json
              jq -er .id backup.json > id.txt
              test -s id.txt
              echo "BACKUP_ID=$(cat id.txt)" >> "$GITHUB_ENV"

          - name: Wait for the backup record
            env:
              PGEDGE_CLIENT_ID: ${{ secrets.PGEDGE_CLIENT_ID }}
              PGEDGE_CLIENT_SECRET: ${{ secrets.PGEDGE_CLIENT_SECRET }}
            run: |
              set -uo pipefail
              for _ in $(seq 1 60)
              do
                  pgedge starfleet managed backup get "$BACKUP_ID" \
                      --timeout 30s -o json > record.json
                  rc=$?
                  if [ "$rc" -ne 0 ]
                  then
                      echo "::error::backup get exited $rc" >&2
                      exit "$rc"
                  fi
                  backup_status=$(jq -r .status record.json)
                  if [ "$backup_status" = "completed" ]
                  then
                      echo "backup $BACKUP_ID completed"
                      exit 0
                  fi
                  if [ "$backup_status" = "failed" ]
                  then
                      echo "::error::backup $BACKUP_ID failed" >&2
                      exit 1
                  fi
                  sleep 30
              done
              echo "::error::backup $BACKUP_ID never settled" >&2
              exit 1

The `pgEdge/pgedge-cli` action installs the release its tag names, so
every run installs the same binary until you change the tag. The
action verifies the release signature and checksum before installing,
and fails the job if either check fails. It runs on Linux and macOS
runners. To install a release other than the tag's, set the action's
`version` input to that release's tag.

The two secrets are step-scoped, so only the steps that call `pgedge`
can read them.

The loop captures the exit status into `rc` before reading any field.
A failed read leaves `record.json` holding whatever was there before.
`jq` would then branch on a stale status.
[Exit codes](exit-codes.md) covers reading the status before the
output.

## Building a GitLab CI Pipeline

This pipeline is a preflight gate on a different runner, in place of
a backup. It spells out the exit status branching that the loop above
compresses. It refuses the deploy unless the database both exists and
reports `available`.

Set `PGEDGE_CLIENT_ID`, `PGEDGE_CLIENT_SECRET` and
`PGEDGE_DATABASE_ID` in the project's CI/CD settings, masked. The
pipeline below then runs as written. GitLab exports each one into the
job's environment under its own name. That is where the CLI reads the
credential pair. The `variables:` block maps the database ID into the
name the job uses. It also pins the release the install script
fetches:

    stages:
      - preflight

    preflight:
      stage: preflight
      image: ubuntu:24.04
      variables:
        DB_ID: $PGEDGE_DATABASE_ID
        PGEDGE_VERSION: v0.5.0-beta.3
      before_script:
        - apt-get update && apt-get install -y ca-certificates curl jq
        - curl -fsSL -o install.sh "https://raw.githubusercontent.com/pgEdge/pgedge-cli/${PGEDGE_VERSION}/install.sh"
        - sh install.sh
      script:
        - |
          set +e
          pgedge starfleet managed database get "$DB_ID" \
              --timeout 30s -o json > db.json
          rc=$?
          set -e
          if [ "$rc" -eq 4 ]
          then
              echo "no database with id $DB_ID" >&2
              exit 1
          elif [ "$rc" -eq 5 ]
          then
              echo "credentials rejected, or the plan excludes this" >&2
              exit 1
          elif [ "$rc" -eq 3 ]
          then
              echo "the API did not answer inside the timeout" >&2
              exit 1
          elif [ "$rc" -ne 0 ]
          then
              echo "database get exited $rc, see the log above" >&2
              exit 1
          fi
          db_status=$(jq -r .status db.json)
          if [ "$db_status" != "available" ]
          then
              echo "database is $db_status, refusing to deploy" >&2
              exit 1
          fi

`set +e` around the call and `set -e` after it let the script read
`rc` at all. Under `set -e` alone, the job would already have ended.
The runner would report a generic failure in place of the CLI's exit
status. Branching on 4 against 5 separates two outcomes. Exit status 4
means the identifier is wrong and a human should look at it. Exit
status 5 usually means the credentials or the plan need fixing. That
is because entitlement refusals land there alongside bad credentials.
A hung token exchange also ends with exit status 5, and stderr says
which of the two happened.

Both runners take the same approach and differ only in mechanics.
GitLab masks its CI/CD variables the same way GitHub masks secrets. In
both, the credential reaches the runner only as job environment
variables. To have the script verify the release signature on GitLab
too, install cosign in the image before `sh install.sh` runs.

If a run fails, [Troubleshooting](troubleshooting.md) is organized by
exit status.

<a id="scheduling-a-poll"></a>

## Scheduling a Recurring Check

A scheduled check needs three things the interactive case gets for
free. It needs a resolvable config file and an absolute path to the
binary. It also needs a request bound short enough that each run ends
before the next one starts.

Cron runs jobs with a minimal environment and skips the login shell,
so set `HOME` and `PATH` in the crontab itself. The following crontab
entry runs a check script every five minutes:

    HOME=/var/lib/pgedge-monitor
    PATH=/usr/local/bin:/usr/bin:/bin

    */5 * * * * /usr/local/bin/pgedge-metrics-check <database-id>

`HOME` locates the config file and the token cache. A job that runs as
a service account needs `HOME` pointing at that account's own
directory. Passing `--config` an absolute path covers the config file
only. The token cache still resolves from `HOME`, so the job keeps
that line either way.

Every invocation picks up a config file at the default path. A
scheduled job authenticates from the file when it is in place, and
`--profile` names which credential inside it to use. Both `--profile`
and `--config` refuse an empty string with exit status 2. An unset
variable fails the run there, before the CLI can silently resolve
`current_profile` and check a different tenant. The GitHub Actions and
GitLab pipelines above supply the credential pair in place of a file.
A `schedule:` trigger turns either one into exactly this kind of
check.

Bound the request as well as the schedule. `--timeout` caps a single
request and defaults to 30 seconds. Set it comfortably below the
schedule interval, so one hung request ends before the next run
starts. The schedule interval and the metrics window are separate
numbers. The window keeps its own length when you shorten the
schedule. On managed, the window has to clear the collector's
publication lag, whatever the interval is.
[Reading Logs and Metrics from a pgEdge Starfleet Managed Database](managed/logs-and-metrics.md)
covers that lag.

## Next Steps

These guides go further:

- [Exit codes](exit-codes.md) carries the full contract and the
  commands that deliberately depart from it.
- [Tasks and async operations](tasks-and-async.md) covers which
  commands take `--wait`, and which you check repeatedly until they
  finish.
- [Connecting an Application to a pgEdge Starfleet Managed Database](managed/connect-an-application.md)
  and
  [Connecting an Application to a pgEdge Starfleet BYOC Database](byoc/connect-an-application.md)
  cover pulling database credentials out of a pipeline safely.
