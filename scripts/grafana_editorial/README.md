# Grafana article revision

This package prepares the revised content for article `1uvwek` without contacting a server. It does not run during application startup or deployment.

The article keeps its existing ID, author, publication date, stored slug, visibility and interactions. Its published canonical path remains:

`/blog/the-ultimate-guide-to-grafana-what-it-is-why-it-used-and-how-to-build-a-complete-monitoring-1uvwek`

Four optional fields support this revision: `canonical_slug` (complete path component including ID), `seo_description`, `content_modified_at`, and `rss_guid` (the previously emitted RSS identity). Metadata/search projections pass these through when present. Older documents omit them. V2 autosaves preserve omitted values using the existing versioned read/CAS pattern; explicit values keep existing save semantics. Authorization and publishing endpoints are unchanged.

## Offline preparation

Export the existing Elasticsearch document with `_source`, `_seq_no`, `_primary_term` and preferably `_id`. Keep backups and real exports outside Git.

```powershell
go run ./scripts/grafana_editorial -input ./local/grafana-original.json -inspect
```

Review the original title, identity, publication/scope and content. Record the printed `original_blog_sha256`. This is the digest of Go's canonical JSON encoding of the blog object, with number precision preserved, rather than the export file's digest. Do not approve a newer export just to bypass a refusal.

```powershell
$originalTitle = (Get-Content ./local/grafana-original.json -Raw | ConvertFrom-Json)._source.blog.blocks[0].data.text
$originalDigest = '<reviewed original_blog_sha256>'
$modifiedAt = '<actual RFC3339 revision time>'
go run ./scripts/grafana_editorial -input ./local/grafana-current.json -original-title $originalTitle -original-blog-sha256 $originalDigest -modified-at $modifiedAt > ./local/grafana-update-review.json
```

The CLI pins the exact article ID, owner and original title. It rejects draft, archived, scheduled, restricted, future-dated or unrelated content, invalid versions, and content differing from the reviewed baseline. A matching already-applied revision produces `already_applied: true` with no update and retains the revision timestamp.

The output is a review envelope, not a full replacement document. Only its `update` object is an Elasticsearch partial-update payload, to be applied manually with its exact `if_seq_no` and `if_primary_term`. The allowlist contains the revised blog, the four optional fields and search-document title/summary/body/tags. Other top-level data stays outside the patch. A version conflict requires a fresh export and review; never drop CAS or replace the full document.

## Reproducible tutorial

`monitoring/` contains the five downloadable files also shipped by the frontend under `/tutorials/grafana-linux/`. Embedded article configuration matches these fixtures. The images are pinned to Grafana 12.3.0, Prometheus 3.7.3 and Node Exporter 1.10.2.

Use a separate Compose project and unused ports. Set `GRAFANA_ADMIN_PASSWORD`; Grafana and Prometheus bind only to loopback. Node Exporter exposes no host port. It reads the Linux host filesystem with a read-only private mount and the host PID namespace. Newly mounted filesystems require restarting the exporter or a separately configured compatible propagation setup.

Docker Desktop measures its Linux VM, not the physical Windows/macOS host. The filesystem selector defaults to `/` where exported. On the tested VM, root was not exported and the real ext4 data filesystem was `/var/lib`; the panel title, image caption and alt text identify that selection. The CPU query uses Grafana's `$__rate_interval`; when querying Prometheus directly, replace it with `5m` and substitute actual instance/mountpoint labels.

`/grafana-linux-dashboard.jpg` is an unaltered browser capture from the real local Grafana dashboard. It appears after the article introduction. No generated or synthetic metric image is used.

## Verification

Passed locally on 22 November 2026:

- Go tests with vet enabled for blog database, blog services, gateway blog search and the offline revision package. Run serially with `GOMAXPROCS=1`, `GOMEMLIMIT=512MiB`, `GOGC=20`: `go test -p 1 -timeout 90s ./microservices/the_monkeys_blog/internal/database ./microservices/the_monkeys_blog/internal/services ./microservices/the_monkeys_gateway/internal/blogsearch ./scripts/grafana_editorial`.
- CLI refusal, precision, partial-update allowlist, CAS and idempotence checks, including future publication/revision dates.
- Actual isolated Docker Desktop Linux VM stack: both scrape targets UP and CPU, memory and selected-filesystem queries returned finite data. All three Grafana panels were inspected through the built-in browser.
- A backed-up local article revision applied with CAS. Original ID, owner, publication date, stored slug, scope and like/bookmark counts remained unchanged. Preparing the applied revision again emitted no update.
- Frontend: 100 targeted regressions, type checking, scoped lint, production build and built-in-browser checks at desktop, 820px, 390px and 320px. Canonical path, one H1, real image/alt text, jump links and fixed-header offsets checked.

Native Ubuntu and the optional remote-server deployment were not executed. The local blog/search containers were not rebuilt; backend behavior is covered by the Go contract tests, while the article UI was checked against the existing local backend and revised local document.

## Deployment ordering

Deploy frontend support and its companion/image assets first, then backend support. Content publication is a separate, reviewed operation after both are available. There is no automatic migration, startup task, new write endpoint or production write command. Production content was not changed by this work.
