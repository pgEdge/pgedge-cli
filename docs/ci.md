# CI and automation

Running the CLI unattended takes three things: credentials supplied
without a prompt, confirmations skipped, and scripts that read output
and exit codes that were designed to be scripted against.

## Supply credentials

Set `PGEDGE_CLIENT_ID` and `PGEDGE_CLIENT_SECRET` from your secret
store. The CLI treats the pair as a profile that lasts one
invocation, so each run authenticates from the secrets alone and
leaves the runner's disk as it found it:

- CI: map the two secrets into the environment of each step that runs
  `pgedge`.
- Docker and Kubernetes: pass both with `docker run --env`, or expose
  a Kubernetes secret to the container as environment variables.

The pair calls the production API unless a command passes
`--api-url`. `--profile` alongside the pair is a usage error, exit
status 2, so leave it off. The [authentication guide](auth-and-profiles.md)
covers the rest of the pair's rules.

A config file suits a host that needs several profiles. Write it to
`~/.pgedge/cli/config.yaml`, or mount it at
`/root/.pgedge/cli/config.yaml` in a container.
`examples/config.yaml` in the repository is the commented template.
Per-invocation flags (`--client-id`, `--client-secret`, `--api-url`)
also work, but argument lists are visible in `ps` on shared hosts,
so prefer the environment variables or the file. The
[configuration guide](configuration.md) covers the file's layout and
the two variables a container run cares about, `HOME` and
`XDG_CONFIG_HOME`.

Pass `--profile` and `--config` a value or leave them off entirely.
Both refuse an empty string at exit 2, so `--profile "$P"` with `$P`
unset fails the step instead of silently resolving `current_profile`
and running against a different tenant.

## Skip prompts deliberately

Destructive commands prompt for confirmation. Pass `--force` to skip
the prompt in automation. This is a per-command decision, not a
global flag, so a script names its own irreversible steps. Without a
terminal to prompt on, a destructive command run without `--force`
exits 2 rather than hanging.

## Read the exit code, never stdout

The exit-code contract is one number, one meaning, across every
module, and the [exit codes guide](exit-codes.md) carries the table
and the deliberate exceptions. The ones that matter for scripts:

- A success carrying no response body prints nothing to stdout, in
  text, json and yaml alike. The acknowledgment is exit 0 and a
  sentence on stderr. Eighteen commands behave this way: most are
  deletes, and the rest are both modules' `rotate-password`,
  `managed database resize`, `byoc backup create`,
  `byoc database restore`, `byoc ingress service deregister` and
  `controlplane cluster join`.
  The rule follows the response rather than the command name, so
  `controlplane database delete`, `controlplane host remove` and both
  modules' `database service remove` do print an object. Branch on
  the exit status, not on output appearing, and see the
  [output guide](output-and-paging.md) for the full picture.
- `pgedge starfleet doctor` and `pgedge controlplane doctor` exit 0
  even when the thing they diagnose is broken. Their job is to
  report, so a script reads their `-o json` fields, never `$?`.
- A plan-entitlement rejection is exit 5, the same class as a bad
  credential, so an auth-retry loop should not spin on it.

Diagnostics stay out of your pipes: `--verbose` and `--debug` write
to stderr, so `-o json | jq` stays clean with either enabled.

The straightforward pattern:

    set -e
    pgedge starfleet managed database rotate-password "$DB_ID" \
        --role app --force --wait

A failure still exits non-zero, so an empty stdout at exit 0 means
the operation succeeded.

## Bound your waits, and know what timed out

`--timeout` bounds a single request in every module (a Go duration,
default 30s, 0 disables). The bound on a wait is a different flag:
commands that take `--wait` poll on `--wait-interval` and give up at
`--wait-timeout`, both in seconds, and in byoc and managed the same
bounds cover `--follow`. controlplane's `--follow` has no overall bound and
ignores `--wait-timeout`: each of its log polls gets its own fixed
30 seconds, independent of `--timeout`, so `--timeout 0` still stops
a poll at 30 seconds and only a `--timeout` below 30 seconds
shortens one.

The [tasks and async operations guide](tasks-and-async.md) covers
which commands take those flags, which signal to trust for each kind
of write, and how to read a task when a wait ends badly.

A timeout is exit 3 whatever produced it, with one exception: a hung
token exchange is exit 5, because authentication is the call that
failed even when a deadline is what failed it. Read stderr for which
bound fired.

Exit 3 does not mean the command is safe to repeat. A read is safe. A
create or delete may already have reached the server before the
bound fired, so repeating it can apply the change twice.

## A dry run is not a gate

Every command that writes to an API takes `--dry-run`, which runs the
CLI's client-side checks and reports the request it would have sent.
A clean dry run means those checks passed, not that the API has
accepted anything, so `cmd --dry-run && cmd` is not a gate on the
real run's exit code. Treat the report as a preflight and read the
list of checks it actually ran. The [dry run guide](dry-run.md)
covers what each command checks, which commands do not take the
flag, and why the report goes to stdout even on commands that
otherwise print nothing.

## A complete GitHub Actions pipeline

A nightly backup of one managed database, start to finish. It
installs the CLI, takes the backup, and polls the backup record to a
terminal status, because `backup create` has no `--wait` and the
task the create spawns reaches `succeeded` while the backup record is
still `pending`. The
[managed backup workflow](managed/backup-restore.md)
explains why the record is the signal.

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
          - uses: sigstore/cosign-installer@v4.1.2

          - name: Install the CLI
            env:
              PGEDGE_VERSION: v0.5.0-beta.2
            run: |
              set -euo pipefail
              curl -fsSL -o install.sh \
                  "https://raw.githubusercontent.com/pgEdge/pgedge-cli/${PGEDGE_VERSION}/install.sh"
              sh install.sh
              echo "$HOME/.local/bin" >> "$GITHUB_PATH"

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

The install step pins one release in `PGEDGE_VERSION`, so every run
installs the same binary until you change it. The script comes from
the same release tag. With cosign installed first, the script
verifies the release signature as well as the checksum. The
`GITHUB_PATH` line covers the script's fallback to `~/.local/bin`
when `/usr/local/bin` is not writable.

The two secrets are step-scoped, so only the steps that call `pgedge`
can read them.

The poll loop captures the exit status into `rc` before reading any
field, because a failed read leaves `record.json` holding whatever was
there before, and `jq` would then branch on a stale status. The
[exit codes guide](exit-codes.md) covers reading the status before the
output.

## A GitLab CI pipeline

A preflight gate rather than a backup, on a different runner, showing
the exit-code branching that the loop above compresses. It refuses the
deploy unless the database both exists and reports `available`.

Set `PGEDGE_CLIENT_ID`, `PGEDGE_CLIENT_SECRET` and
`PGEDGE_DATABASE_ID` in the project's CI/CD settings, masked, and
the pipeline below runs as written. GitLab exports each one into the job's
environment under its own name, which is where the CLI reads the
credential pair. The `variables:` block maps the database ID into the
name the job uses, and pins the release the install script fetches:

    stages:
      - preflight

    preflight:
      stage: preflight
      image: ubuntu:24.04
      variables:
        DB_ID: $PGEDGE_DATABASE_ID
        PGEDGE_VERSION: v0.5.0-beta.2
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

`set +e` around the call and `set -e` after it is what lets the script
read `rc` at all. Under `set -e` alone the job would already have
ended, with the runner reporting a generic failure and none of the
distinctions the CLI makes. Branching on 4 against 5 separates two
outcomes: the first means the identifier is wrong and a human should
look at it, the second means no retry will ever help, because
entitlement refusals land there alongside bad credentials.

The two runners differ in mechanics rather than in approach. GitLab
masks its CI/CD variables the same way GitHub masks secrets, and in
both the credential reaches the runner only as job environment
variables. To have the script verify
the release signature on GitLab too, install cosign in the image
before `sh install.sh` runs.

If a run fails, the [troubleshooting guide](troubleshooting.md) is
organized by exit code.

## Scheduling a poll

A scheduled poll needs three things the interactive case gets for
free: a resolvable config file, an absolute path to the binary, and a
request bound short enough that one run cannot overlap the next.

Cron runs with a minimal environment, so set both in the crontab
rather than relying on a login shell. The following crontab entry runs
a check script every five minutes:

    HOME=/var/lib/pgedge-monitor
    PATH=/usr/local/bin:/usr/bin:/bin

    */5 * * * * /usr/local/bin/pgedge-metrics-check <database-id>

`HOME` is what locates the config file and the token cache, so a job
that runs as a service account needs it pointing at that account's
own directory. Passing `--config` an absolute path covers the config
file only. The token cache still resolves from `HOME`, so the job
keeps that line either way.

A config file at the default path is picked up by every invocation, so
a scheduled job authenticates with no flag once the file is in place,
and `--profile` names which credential inside it to use. Both
`--profile` and `--config` refuse an empty string at exit 2, so an
unset variable fails the run instead of silently resolving
`current_profile` and polling a different tenant. The GitHub Actions
and GitLab pipelines above supply the credential pair instead of a
file, and a `schedule:` trigger turns either one into exactly this
poll.

Bound the request as well as the schedule. `--timeout` caps a single
request and defaults to 30 seconds, and setting it comfortably below
the schedule interval keeps one hung poll from running into the next.
The schedule interval and the metrics window are separate numbers, so
shortening the schedule does not let you shorten the window with it.
On managed the window has to clear the collector's publication lag
whatever the interval is, and the
[managed logs and metrics guide](managed/logs-and-metrics.md) covers
that lag.

## Next steps

- The [exit codes guide](exit-codes.md) carries the full contract and
  the commands that deliberately depart from it.
- The [tasks and async operations guide](tasks-and-async.md) covers
  which commands take `--wait` and which need polling instead.
- The connect an application guides for
  [Managed](managed/connect-an-application.md) and
  [BYOC](byoc/connect-an-application.md) cover pulling database
  credentials out of a pipeline safely.
