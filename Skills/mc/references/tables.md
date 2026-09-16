# AIStor Iceberg Tables — `mc table`

`mc table` manages **Apache Iceberg** tables served by AIStor's built-in
Iceberg REST catalog — a control plane distinct from the S3 object commands. It
is AIStor-specific. Run `mc table --help` and `mc table <group> --help` for the
exact subcommands and flags. The hierarchy is:

```text
warehouse  →  namespace  →  table  (with views, tags, sharing, replication)
```

Targets are a **single slash-path**, not separate arguments:
`ALIAS/WAREHOUSE`, `ALIAS/WAREHOUSE/NAMESPACE`, `ALIAS/WAREHOUSE/NAMESPACE/TABLE`
— each level adds a path segment.

## Warehouses

A warehouse is the top-level container backed by a bucket.

```sh
mc table warehouse create myminio/mywarehouse
mc table warehouse list   myminio
mc table warehouse remove myminio/mywarehouse         # irreversible — confirm
mc table warehouse maintenance …                      # compaction / cleanup policy
```

Note: an AIStor Tables warehouse bucket is special — it rejects ordinary bucket
replication (`mc replicate`); use table replication (below) instead.

## Namespaces

Logical grouping within a warehouse (like a database/schema):

```sh
mc table namespace create myminio/mywarehouse/mynamespace
mc table namespace list   myminio/mywarehouse
mc table namespace info   myminio/mywarehouse/mynamespace
mc table namespace delete myminio/mywarehouse/mynamespace
```

## Tables

```sh
# --schema takes the JSON schema as a string, not a filename — inline it or read a file in
mc table create myminio/mywarehouse/mynamespace/mytable --schema "$(cat schema.json)"
mc table list   myminio/mywarehouse/mynamespace
mc table show   myminio/mywarehouse/mynamespace/mytable        # schema, snapshots, metadata
mc table rename myminio/mywarehouse/mynamespace/mytable …
mc table delete myminio/mywarehouse/mynamespace/mytable        # drops the table — irreversible
mc table register …                                            # register an existing Iceberg table
mc table tag set|list|remove …
mc table encrypt set|info|clear …
```

Views: `mc table view create|list|show|drop|exists`.

## Catalog, migration, sharing, replication

- `mc table catalog export` — export the catalog to a zip file (`export` is its
  only subcommand); `mc table migrate` imports from an external catalog
  (`rest`, `hive`, `glue`, `sql`, `nessie`).
- `mc table share` — issue and manage shares and share tokens so external
  clients can read tables (`share create|list|info|update|remove`,
  `share token create|list|remove`).
- `mc table replicate` — replicate tables/catalog to another site
  (`backfill`, `failover`, `resync`, `status`); the Tables-aware counterpart to
  object/site replication.
- Maintenance (compaction / snapshot-expiry) is **configured per warehouse** via
  `mc table warehouse maintenance {info,set,update}`; `mc table maintenance
  status` shows a table's or warehouse's maintenance-job status (its only
  subcommand — it does not run or configure maintenance itself).

## Agent guidance

The target is one slash-path whose depth matches the level you're acting on —
when unsure, run the subcommand's `--help` rather than guessing. `delete` /
`remove` / `drop` on any level (warehouse, namespace, table, view) discards
metadata irreversibly; confirm with the user first.
