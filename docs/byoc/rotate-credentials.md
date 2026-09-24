# Rotate a BYOC database password

The CLI rotates a BYOC database's built-in role passwords.

`pgedge starfleet byoc database rotate-password` rotates a built-in
role's password. `--role` takes one of the built-in roles the
command's `--help` lists, and any other value is refused before a
request is sent. Rotation breaks any session still using the old
password, so it prompts unless `--force` is given.

The database and its cluster must both be `available` before a
rotation is accepted, and a rotation attempted while a previous one
is still running is refused as a busy resource: exit 1 with a message
saying to wait and retry. Run `database get` before retrying, because
a `failed` database returns the same message and never becomes
available.

1. Rotate, and wait for the outcome rather than the acceptance:

        pgedge starfleet byoc database rotate-password <db-id> \
            --role app --force --wait

    Without `--wait`, exit 0 means the API accepted the rotation and
    started the work, not that the running database is using the new
    password.

2. Read the new password back. It is not printed by the rotation, and
   it comes back from a plain `database get`, which has no
   `--user-type`:

        pgedge starfleet byoc database get <db-id> -o json

## Next steps

- The [manage BYOC databases](databases.md) document covers reading
  the credentials back, the built-in roles and the statuses a
  database moves through.
- The [starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists the flags.
