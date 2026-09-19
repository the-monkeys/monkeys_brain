# Cluster administration — `mc admin`

`mc admin` is the control plane for a **MinIO AIStor** server — not the S3 data
plane. These commands use the AIStor admin API and only work against a MinIO
AIStor server (never AWS S3, and not the general S3 data plane). The alias must
hold **admin-level** credentials. Run
`mc admin --help` and `mc admin <group> --help` for the exact, version-matched
subcommands and flags; the groups below are the map.

## Observe & diagnose (safe, read-mostly)

| Group | What |
| --- | --- |
| `mc admin info ALIAS` | Cluster status: nodes, drives, capacity, online/offline. Start here. `--json` for a full snapshot. |
| `mc admin trace ALIAS` | Live request trace across the cluster; filter with `--call`, `--status-code`, `--errors`, `--funcname`. |
| `mc admin logs ALIAS` | Recent server log entries. |
| `mc admin scanner ALIAS` | Object-scanner status/metrics. |
| `mc admin prometheus generate ALIAS` | Emit a Prometheus scrape config for the cluster's metrics endpoints. |
| `mc admin cluster info \| iam \| bucket` | Inspect cluster; export/import IAM and bucket metadata for backup/migration. |
| `mc admin profile` / `mc support inspect` | Capture pprof profiles / inspect on-disk files for support cases. (`mc admin inspect` is deprecated — use `mc support inspect`.) |

## Identity & access

| Group | What |
| --- | --- |
| `mc admin user` | Create/list/enable/disable IAM users, set passwords. |
| `mc admin group` | Manage groups and membership. |
| `mc admin policy` | Create/attach/detach **IAM** policies (JSON). Distinct from `mc anonymous`, which is public-bucket access. |
| `mc admin accesskey` | Service accounts / access keys for a user (create, list, edit, remove). |
| `mc idp {ldap,openid} …` | Configure external identity providers. (Top-level; `mc admin idp` is deprecated and only prints a redirect.) |

## Configuration

`mc admin config` manages server config key/value groups:

```sh
mc admin config get ALIAS                 # all groups
mc admin config get ALIAS notify_webhook  # one group's keys
mc admin config set ALIAS notify_webhook:1 endpoint="https://…"
mc admin config export ALIAS > cfg.txt    # back up before changing
mc admin service restart ALIAS            # many config changes need a restart to apply
```

`mc admin service` has exactly two subcommands: `restart` (bounces the cluster —
disruptive) and `unfreeze` (resumes S3 API calls after a freeze). There is no
`stop`. `restart` is disruptive — confirm with the user before running it.

## Healing, capacity & topology (operational — confirm)

| Group | What | Caution |
| --- | --- | --- |
| `mc admin heal ALIAS` | Heal objects/metadata (usually automatic; manual runs are I/O-heavy). | Load |
| `mc admin decom start\|status\|cancel` | Decommission a server pool so it can be removed. | Moves all data off a pool — long, irreversible once complete |
| `mc admin rebalance start\|status\|stop` | Rebalance data across pools. | Heavy background I/O |
| `mc admin cordon` / `uncordon` | Drain/undrain a node ahead of maintenance. | Shifts traffic |

## KMS, tiers, site replication

- `mc admin kms key {create,list,status}` — server-managed KMS keys for SSE-KMS.
- `mc admin tier add|ls|edit|rm` — remote tier targets (S3/GCS/Azure/MinIO) that
  ILM transition rules move cold data to. Same targets as `mc ilm tier`.
- `mc admin replicate add|info|status|resync` — **site replication**: keeps
  buckets, objects, IAM, and config in sync across whole clusters. This is the
  cluster-wide sibling of the per-bucket `mc replicate`
  (`references/data-management.md`).

## SUBNET & support

`mc license register` registers the cluster with MinIO SUBNET, and `mc support`
(`diag`, `perf`, `inspect`, `profile`, `callhome`, `top`, …) runs health checks
and uploads diagnostics for commercial support. (`mc admin subnet` is
deprecated — its register/health functions moved to `mc license` / `mc support`.)
These transmit cluster data to MinIO — get user consent
before running.
