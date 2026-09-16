# Lifecycle, replication, inventory & batch

Server-side data management: rules the cluster executes on its own, versus the
client-driven copies in `references/objects.md`. Run `mc <cmd> --help` for exact
flags.

## Lifecycle (ILM) — `mc ilm`

Rules the server applies to age out, transition (tier), or expire objects.
Subcommands: `rule` (the rules), `tier` (remote tier targets — see below), and
`restore` (rehydrate a tiered object).

```sh
mc ilm rule add  myminio/mybucket --expire-days 90
mc ilm rule add  myminio/mybucket --transition-days 30 --transition-tier WARM-TIER
mc ilm rule ls   myminio/mybucket
mc ilm rule ls   myminio/mybucket --json          # inspect rule IDs
mc ilm rule rm   myminio/mybucket --id <RULE_ID>
mc ilm rule export myminio/mybucket > rules.json   # back up before editing
mc ilm rule import myminio/mybucket < rules.json
```

Expiration lifecycle rules delete data on the server's schedule — an expire rule
is a **deferred destructive operation**. Export existing rules before changing
them, and confirm any rule that shortens retention or adds expiry.

Tiering targets are defined with `mc ilm tier add|ls|update` (this is the same
mechanism surfaced as `mc admin tier`); a transition rule then points at a tier
name.

## Replication — `mc replicate`

Asynchronous bucket-to-bucket replication (server-to-server). The remote target
must first be registered with `mc admin bucket remote add`, then:

```sh
# --remote-bucket embeds credentials in argv (visible in `ps`/history) — build it
# from a secret source, e.g. --remote-bucket "https://${KEY}:${SECRET}@peer/mybucket"
mc replicate add    myminio/mybucket --remote-bucket "$REMOTE_URL" --priority 1
mc replicate ls     myminio/mybucket
mc replicate status myminio/mybucket           # health + metrics
mc replicate backlog myminio/mybucket          # what's pending
mc replicate export myminio/mybucket > repl.json
mc replicate rm     myminio/mybucket --all --force   # removes ALL rules — irreversible config change
mc replicate resync start myminio/mybucket     # re-replicate existing objects
```

`mc replicate resync` (subcommands `start`/`status`/`cancel`) re-drives
replication of already-present objects —
expensive; understand the data volume first. For whole-cluster (all buckets,
IAM, config) replication, use **site replication** under
`mc admin replicate` (`references/admin.md`), not this command.

## Inventory — `mc inventory`

Scheduled reports listing every object in a bucket (for reconciliation/billing).
Subcommands include `generate`/`put`, `get`, `list`, `status`, `suspend`,
`resume`, `cancel`, `delete`. Reports write to a destination bucket/prefix on a
schedule you set.

## Batch jobs — `mc batch`

Server-side batch jobs defined by a YAML manifest (`replicate`, `keyrotate`,
`expire`). The job runs entirely on the server, so it survives client
disconnect and handles huge object counts:

```sh
mc batch generate myminio replicate > job.yaml    # scaffold a manifest
# edit job.yaml
mc batch start    myminio job.yaml                # returns a job id
mc batch status   myminio <job-id>
mc batch list     myminio
mc batch describe myminio <job-id>
mc batch cancel   myminio <job-id>
```

Prefer `mc batch` over a long client-side `mc mirror`/`cp` for large migrations:
it's resumable, observable, and doesn't depend on the agent's process staying
alive. A `batch expire` job is destructive — review the manifest before `start`.
