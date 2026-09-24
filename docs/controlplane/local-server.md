# Stand up a local Control Plane

This walkthrough gets a Control Plane running on your own machine,
initialized and answering the CLI. The controlplane module installs
nothing and starts nothing, so every controlplane command needs a
server already running somewhere, and this one puts it on localhost
with Docker.

The server is published as a container image,
`ghcr.io/pgedge/control-plane:latest`, reads a JSON config whose one
required key is `data_dir`, and serves plain HTTP on port 3000
unless the config says otherwise.

1. Start the server. Three of these arguments carry requirements
   the server does not check at startup, explained below:

        CP_DATA="$HOME/cpdata"
        mkdir -p "$CP_DATA"
        printf '{"data_dir":"%s"}\n' "$CP_DATA" > config.json
        docker run -d --name cp --network host \
            -v /var/run/docker.sock:/var/run/docker.sock \
            -v "$CP_DATA:$CP_DATA" \
            -v "$(pwd)/config.json:/config.json:ro" \
            ghcr.io/pgedge/control-plane:latest run -c /config.json

    Writing the config with `printf` keeps one variable in the
    config, in the bind mount and inside the container, which the
    third requirement below depends on.

2. Point the CLI at it and confirm the server answers:

        pgedge controlplane config set --base-url http://localhost:3000
        pgedge controlplane doctor -o json

    Expect `reachable` to be `true`. A reachable server is not yet an
    initialized one: until the next step, every command that reads or writes
    cluster state answers a 409.

3. Initialize the cluster:

        pgedge controlplane cluster init

    This creates the cluster and prints a TOKEN and SERVER-URLS pair.
    Add `--cluster-id` to name the cluster yourself rather than take
    the server-generated id.

4. Join each further host. Point `--base-url` at the new host, which
   must be running its own Control Plane and must not have been
   initialized, and give it the token plus at least one existing
   member to contact:

        pgedge controlplane cluster join --base-url http://host-2:3000 \
            --token PGEDGE-abc --server-url http://host-1:3000

    `--server-url` is repeatable, so list several members when you
    have them. Both flags are checked when the command runs rather
    than by cobra, so omitting either is a usage error at exit 2, and
    a token the server rejects is exit 5. Reprint the pair at any
    time with `pgedge controlplane cluster join-token`, and confirm
    the result with `pgedge controlplane cluster info` and `pgedge
    controlplane host list`.

From here, `pgedge controlplane database init` and `pgedge
controlplane database create` work as the
[Control Plane databases guide](databases.md) shows.

The three requirements the server only enforces later:

- The Docker socket. The Control Plane drives the host's Docker
  Swarm, so it needs `/var/run/docker.sock`. Without it, `cluster
  init` fails with a 500 naming the Docker daemon, and the server
  then exits and will not start again: it re-runs the same setup
  from `data_dir` on every boot and dies the same way. Emptying
  `data_dir` is the way back.
- `--network host`. The `pg_hba.conf` the Control Plane generates
  trusts the host and each database's own overlay subnet, so a
  Control Plane holding an ordinary container address cannot
  authenticate to the Postgres it just created. Host networking also
  means the server takes the host's port 3000. If something already
  holds it, the container dies with a bind error visible in `docker
  logs`. Publishing with `-p 3000:3000` instead gets you `version`,
  `doctor` and the uninitialized 409s, and not a working database
  creation, for the pg_hba reason above.
- `data_dir` must name the same absolute path on the host and inside
  the container. The Control Plane computes bind-mount sources from
  it and hands them to Swarm, which resolves them on the host, so a
  container-only path survives `cluster init` and fails part-way
  through database creation. On Docker Desktop the path must also be
  one Docker shares. A directory under your home directory is, and
  a root-level path like `/cpdata` is not.

So a Control Plane that `controlplane doctor` calls reachable is not
yet proof of one that can create a database. The
[Control Plane databases guide](databases.md) covers the
spec workflow, and the [configuration guide](../configuration.md)
covers the multi-URL failover a `base_urls` list gives you.

## Next steps

- The [Control Plane databases guide](databases.md)
  covers writing a spec and running the database lifecycle on the
  cluster this server now hosts.
- The [overview](../index.md) shows the controlplane database init and create
  pipeline this server can now run.
- The [troubleshooting guide](../troubleshooting.md) covers the controlplane
  doctor fields and the exit-code contract.
- The [controlplane command reference](../reference/controlplane.md) lists every controlplane
  command and its flags.
