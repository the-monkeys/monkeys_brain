# Connecting: aliases, credentials, TLS

Every remote command resolves its first path segment to an **alias**: a stored
`{URL, accessKey, secretKey, api, ...}`. There is no `s3://` scheme — the alias
name is the scheme.

## Discover before you create

```sh
mc alias list                 # all aliases; secret keys are redacted
mc alias list myminio         # one alias
mc alias list --json          # machine-readable
```

Env-defined aliases (below) also appear in `mc alias list`, tagged with source
`env`.

## Environment aliases — the agent default

`mc` reads any variable named `MC_HOST_<alias>` and exposes `<alias>` as if it
were configured. It never writes credentials to `mc`'s config file, which makes
it the preferred form for agents and ephemeral/CI use:

```sh
# Prefer injecting the value from a secret manager / CI secret, not a literal:
export MC_HOST_myminio="https://${MINIO_ACCESS_KEY}:${MINIO_SECRET_KEY}@minio.example.net:9000"
mc ls myminio/
```

Caveat: an environment variable is still a secret in memory — a literal
`export …` typed at a shell is saved to shell history, and the value is visible
in the process environment (`/proc/<pid>/environ`, `ps e`). Set it from a secret
source, avoid the plaintext literal, and don't echo it into logs.

URL forms accepted in the value:

```text
https://ACCESS_KEY:SECRET_KEY@HOST[:PORT]                    # long-term keys
https://ACCESS_KEY:SECRET_KEY:SESSION_TOKEN@HOST[:PORT]      # STS / temporary creds
https://HOST:PORT                                            # anonymous (public buckets)
```

The scheme (`http`/`https`) is part of the value. Special characters in keys
must be URL-encoded. To feed several aliases from a file, point
`MC_CONFIG_ENV_FILE` at a file of `MC_HOST_<alias>=…` lines.

## Persisted aliases

Only when the user wants the alias saved to `~/.mc/config.json`:

```sh
mc alias set myminio https://minio.example.net:9000 ACCESS_KEY SECRET_KEY
mc alias set mys3 https://s3.amazonaws.com ACCESS_KEY SECRET_KEY --api S3v4 --path off
mc alias remove myminio
```

Passing keys as arguments leaks them into shell history — prefer prompting
(omit the keys and `mc` asks) or piping them in. `mc alias set` verifies the
endpoint is reachable before saving; it fails fast on a bad URL or credentials.

## Config location

`~/.mc/config.json` by default (on Windows, `%USERPROFILE%\mc\`). Override the
whole directory with the global flag `--config-dir <path>` or `MC_CONFIG_DIR`.
Give each isolated agent run its own `--config-dir` to avoid clobbering a
user's real config.

## TLS and connectivity

- `--insecure` skips certificate verification (self-signed dev servers). Never
  make this the default; state clearly when it's needed.
- `--resolve HOST:PORT=IP` overrides DNS for one host (e.g. hitting a specific
  node behind a load balancer).
- `mc ready ALIAS` reports whether the cluster is ready to serve; `mc ping
  ALIAS` measures per-endpoint latency. Use these to validate a new alias.

## Other S3 providers

`mc` works against AWS S3 and other S3-compatible stores, not just MinIO.
Choose bucket addressing with `--path`:

- `--path auto` (default) → let `mc` decide; resolves correctly for AWS S3 and
  most managed providers, so you rarely need to set this at all.
- `--path off` → force virtual-hosted / DNS-style (`bucket.host`).
- `--path on` → force path-style (`host/bucket`) — typical for MinIO AIStor and
  localhost.

`mc admin` commands are MinIO/AIStor-specific and will not work against AWS S3.
