# Machine-readable output & scripting

## `--json` is JSON Lines (NDJSON)

`--json` (global; also `MC_JSON=true`) makes `mc` print **one JSON object per
line**, not a single JSON document. Boolean env vars must be a Go
`strconv.ParseBool` value — `true`/`1` (or `false`/`0`); `MC_JSON=on` is **not**
accepted and makes every `mc` command error out. This is the single most
important fact for parsing `mc`:

```sh
mc ls --json myminio/mybucket | jq -c '.'          # per-record, streaming
```

```python
import sys, json
for line in sys.stdin:                              # decode one record at a time
    obj = json.loads(line)
    ...                                             # handle obj, don't accumulate
```

Do **not** slurp the whole stream into one document — `json.loads(f.read())`
fails after the first record because the lines aren't a single JSON value. `jq`
already reads NDJSON one record at a time: use `jq -c '.'` for per-record
streaming, and reach for `jq -s '.'` only when you deliberately want every
record collected into one array (which loses the streaming benefit). For
streaming commands (`ls`, `find`, `cp`, `mirror`, `watch`) each line is emitted
as the event happens, so you can consume incrementally.

Every record carries `"status"`: `"success"` or `"error"`. On failure the
record includes `error.message` / `error.cause` and `mc` also exits non-zero:

```sh
mc stat --json myminio/mybucket/key | jq -e '.status == "success"'
```

## Exit codes drive control flow

`mc` exits `0` on success and non-zero on any failure. Branch on the exit code,
never on scraping human-readable stdout:

```sh
if mc stat myminio/mybucket/key >/dev/null 2>&1; then
  echo "exists"
fi
```

Note streaming commands: `mc cp`/`mirror` return non-zero if any item failed.

## Non-interactive operation

- Some commands prompt for confirmation on dangerous actions
  (`rm --recursive`, `rb`). The required flags (`--force`, `--dangerous`) are
  what let them run unattended — see `references/objects.md`. If a command still
  blocks on a prompt, that is a signal to stop and confirm with the user, not to
  auto-answer.
- `mc alias set` with keys omitted prompts on stdin; provide keys via args (they
  hit shell history) or pipe them in for unattended runs.
- `--quiet` (`-q`, or `MC_QUIET=true`) removes progress bars and spinners — use it
  in logs/CI so output is deterministic. `--no-color` / `MC_NO_COLOR=true` strips
  ANSI. `mc` auto-disables color when stdout is not a TTY. (As with `MC_JSON`,
  these env vars take `true`/`1`, not `on`.)

## Useful shapes

- `mc ls --json` → per object: `key`, `size`, `lastModified`, `etag`,
  `type` (`file`/`folder`), `storageClass`, plus `versionId` with `--versions`.
- `mc stat --json` → object/bucket metadata, user metadata, tags, encryption,
  retention.
- `mc du --json` → cumulative size per prefix.
- `mc diff --json` → one record per differing path with a status flag.

When a shape matters, capture one line and inspect it (`mc ls --json … | head -1
| jq .`) rather than assuming field names — they can vary by command and server
version.
