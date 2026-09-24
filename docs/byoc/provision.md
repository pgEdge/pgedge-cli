# Provision a BYOC cluster and database

pgEdge Starfleet BYOC provisions a cluster and a database in your own
cloud account, on clusters and nodes you create, from a clean slate to
`available`. The sequence is: verify the connection, settle the
backup store, create the cluster, then create the database.

You need a cloud account already attached to your tenant (`pgedge
starfleet byoc cloud-account list`), and a target region string such
as `us-west-2`. If that list comes back empty, or the tenant has no
cloud account yet, start with the
[Register a BYOC cloud account](cloud-accounts.md) guide. Return here
when you finish.

1. Verify authentication before provisioning anything:

        pgedge starfleet doctor -o json

    Expect `auth.authenticated` and `tenant.resolved` both `true`,
    and stop otherwise. A resolved tenant proves the server accepts
    the credential. The [Troubleshooting](../troubleshooting.md)
    guide explains the other fields.

2. Check for an existing backup store, and read the exit code
   before reading the array:

        pgedge starfleet byoc backup-store list -o json

    This command is plan-gated. A failed read prints nothing on
    stdout. A script that checks only for a non-empty array misses
    the failure and creates a duplicate store. On exit 0 with a
    non-empty array, take `id` from the first result and skip the
    next step.

3. If none exists, create a backup store and take `id` from the
   response:

        pgedge starfleet byoc backup-store create \
            --name my-store \
            --cloud-account-id <cloud-account-id> \
            --region us-west-2 \
            -o json

4. Create the cluster. The response is the cluster itself, so take
   `id` from it directly. There is no task wrapper. Passing
   `--wait` blocks the command until provisioning finishes:

        pgedge starfleet byoc cluster create \
            --name my-cluster \
            --cloud-account-id <cloud-account-id> \
            --regions us-west-2 \
            --node-location public \
            --backup-store-id <backup-store-id> \
            --node name=n1,instance-type=r7g.medium,volume-size=30 \
            --network cidr=10.4.0.0/16,public-subnets=10.4.1.0/24 \
            --firewall-rule name=postgres,port=5432,sources=0.0.0.0/0 \
            --wait --wait-timeout 3600 --wait-interval 15 \
            -o json

    Pick a CIDR that does not overlap other clusters in the same
    account. On GCP, use the `subnets` network key, on public and
    private clusters alike. The API rejects `private_subnets` on a
    Google cluster, and its response names `subnets` instead.

5. Confirm the cluster is ready, whether you waited or not:

        pgedge starfleet byoc cluster get <cluster-id> -o json

    Expect `"status": "available"`. Passing `--wait` reports the
    task reaching a terminal state. This read confirms the cluster
    itself.

6. Create the database on the cluster, again with `--wait`:

        pgedge starfleet byoc database create \
            --name my-db \
            --cluster-id <cluster-id> \
            --wait \
            -o json

7. Confirm the database:

        pgedge starfleet byoc database get <db-id> -o json

    Expect `"status": "available"`.

The cluster flags above are the short form. The
[Manage BYOC clusters](clusters.md) guide covers node location, the
per-cloud subnet keys, firewall rules, volume sizing, updates, shares
and deletion.

The [Deploy BYOC services](services.md) guide covers putting MCP and
RAG services onto the database. The
[Expose a BYOC service](expose-service.md) guide covers reaching one
on a private cluster through an ingress.

## Next steps

- The [Manage BYOC clusters](clusters.md) guide covers the cluster
  you built, including updates and deletion.
- The [Manage BYOC databases](databases.md) guide covers the
  database's whole lifecycle, its credentials and its built-in
  roles.
- The [Deploy BYOC services](services.md) guide covers putting an
  MCP, RAG or PostgREST service on the new database.
- The [Rotate a BYOC database password](rotate-credentials.md)
  guide covers the built-in roles' passwords on the new database.
- The [CI and automation](../ci.md) guide covers running this
  sequence unattended.
- The
  [pgedge starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
