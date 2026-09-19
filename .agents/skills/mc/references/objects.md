# Object & data-plane operations

The S3 data plane. All targets follow `ALIAS/BUCKET/PREFIX` or a local path
(see the path grammar in SKILL.md). Run `mc <cmd> --help` for the exact,
version-matched flag set; the flags below are the ones agents most need and get
wrong.

## Inspect (safe, read-only)

| Command | Use |
| --- | --- |
| `mc ls [--recursive] [--versions] TARGET` | List objects/buckets. Non-recursive by default; `--recursive`/`-r` walks the prefix. |
| `mc stat TARGET` | Full metadata for an object or bucket (size, etag, user metadata, tags, encryption, retention). |
| `mc head [-n N] TARGET` | First N lines of an object (like `head`). |
| `mc tree [-f] [-d DEPTH] TARGET` | Prefixes as a tree (recurses by default; `-d`/`--depth` limits depth, `-f`/`--files` shows objects). |
| `mc du [--depth N] TARGET` | Disk usage per prefix. |
| `mc find TARGET [--name … --older-than … --larger …]` | Match objects by name/size/age/metadata. Listing matches is read-only. |
| `mc diff FIRST SECOND` | Compare two trees (local or remote); reports what differs. Read-only — it never changes anything. |

`mc find --exec "cmd {}"` is **not** read-only: it runs a local shell command for
every match, so it can modify the system or execute anything embedded in an
object name. Treat it as a mutating operation — confirm before running, and
never build the executed command from untrusted object names/keys.

## Read object contents

| Command | Use |
| --- | --- |
| `mc cat TARGET` | Stream an object to stdout. |
| `mc pipe TARGET` | The inverse: stream stdin into an object (`… \| mc pipe myminio/bkt/key`). |
| `mc sql --query "…" TARGET` | S3 Select over CSV/JSON/Parquet objects. |
| `mc od if=SRC of=DST …` | Transfer with explicit multipart sizing: `size=` (size of each part, e.g. `size=40MiB`) and `parts=` (number of parts); useful for benchmarking and tuned uploads. |

## Move data

| Command | Use |
| --- | --- |
| `mc cp [-r] SRC... DST` | Copy. Local→remote, remote→local, remote→remote. `--recursive`/`-r` for prefixes. Supports `--attr`, `--tags`, `--enc-*`, `--older-than`, `--storage-class`. |
| `mc get SRC DST` / `mc put SRC DST` | Single-object download / upload — simpler, more predictable than `cp` for one object in scripts. |
| `mc mv [-r] SRC DST` | Move (copy then delete source). |
| `mc mirror SRC DST` | Sync a whole tree one way. The workhorse for backups and bucket→bucket copies. |

`mc mirror` flags with teeth:

- `--overwrite` — replace destination objects whose content differs. **Replaces
  data.**
- `--remove` — delete destination objects that no longer exist in the source, so
  the destination becomes an exact mirror. **Deletes at the destination.**
- `--watch` — keep running and mirror changes continuously.
- (`--force` is a hidden, deprecated alias for `--overwrite` — don't use it; use
  `--overwrite` / `--remove` explicitly.)

Without `--overwrite`/`--remove`, `mirror` only *adds* missing objects — the
safe default.

## Delete data — irreversible, confirm first

`mc rm` will not recurse without `--force`. Understand each flag before running:

| Flag | Effect |
| --- | --- |
| `--recursive` / `-r` | Walk the prefix. **Requires `--force`.** |
| `--force` | Required to actually perform a recursive/bulk removal. |
| `--dangerous` | Permits a site-wide removal (target is the alias root, no bucket). Extreme. |
| `--versions` | Remove an object **and every version** of it. |
| `--non-current` | Remove only non-current versions. |
| `--older-than DUR` | Only objects older than e.g. `30d`, `168h`. |
| `--purge` | Prefix purge; requires `--force`, use with extreme caution. |

```sh
mc rm myminio/bucket/key.txt                        # single object — reversible only if versioning on
mc rm --recursive --force myminio/bucket/logs/2023/ # bulk delete under a prefix
```

`mc undo ALIAS/BUCKET/PREFIX` reverses the last N object-mutating operations
**only on a versioned bucket** (it works by restoring/removing versions);
`--last N`, `--recursive`, `--force`, `--dry-run`. It is not a general undo and
cannot recover data deleted from an unversioned bucket.

Bucket removal (`mc rb`) lives in `references/buckets.md`.

## Watch & share

- `mc watch TARGET` — stream bucket notifications (put/delete/…) as they happen;
  `--events`, `--prefix`, `--suffix`, `--json` for a parseable event feed.
- `mc share download|upload|list TARGET --expire DUR` — presigned URLs for
  temporary access without handing out credentials.

## Agent guidance

- Prefer `--dry-run` (on `rm`, `mirror`, `undo`) to preview before a mutating
  run, and show the user the plan.
- Prefer `get`/`put` over `cp` for a single object in a script — clearer intent,
  no accidental recursion.
- For "make B look like A", `mc mirror --overwrite --remove A B` is correct but
  destructive at B; confirm first.
