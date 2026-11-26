# Local moderation-check performance — 26 November 2026

The user-service `Store.Check` now batches scanner facts for visible targets inside its existing read-only, repeatable-read transaction. It retains the complete `content_media_owners` view and pairs kind with target ID. Duplicate output order, missing/inaccessible targets, current actor and DOB, rating revisions and reveal permissions retain their existing behavior. One unique target uses the original scalar scanner query. Media and mutation callers still perform their original scalar checks.

No API/protobuf, schema, service, cache lifetime, gateway deadline or response contract changed. Existing frontend/backend callers remain compatible. This introduces no authorization cache. Query, scan, iteration and context errors fail closed.

## Evidence and scope

Before source: `fee247383df52dd86671930a663aa867547cf78f`. Measurements used Docker Desktop's local Linux daemon, the internal restore-local network and local development fixtures only. No production requests or deployment occurred.

Three serial read-only `EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON)` probes over the same twelve local blog IDs, two-second statement timeout and no parallel workers, gave median scanner execution times of 868.683 ms for twelve scalar queries combined and 59.034 ms for the batch join. The broad ownership view was evaluated twelve times versus once. Raw plans were not retained; these numbers are summarized from captured probe output. The performance dataset had 104 storage assets and zero scanner flags. Positive-flag correctness is covered by the integration fixtures; positive-flag production-scale performance was not measured.

A single anonymous local API GET per explicit path, before/after rebuilding only the user service, measured homepage latest 1,021.32 → 267.76 ms, trending 460.41 → 146.24 ms, discussions 541.12 → 110.36 ms, groups 339.24 → 103.60 ms and events 357.24 → 119.46 ms. Successful responses retained their measured byte counts. These are single probes, not percentiles or capacity estimates. The 500-item metadata request changed from HTTP 503 in 6,612.14 ms to HTTP 200 in 2,390.70 ms.

The paired frontend repository contains raw before/after samples for 38 page routes, five samples each at concurrency one and four, with separate sequential warmups. Those HTTP page results include both this backend optimization and the frontend icon optimization; they are not field LCP or real production traffic measurements.

## Verification

- Intentional RED query-count regression failed against the original implementation; focused scanner tests passed after batching.
- `go test ./common/moderation -run '^TestStoreCheck(BatchesScanner|Scanner)' -count=1 -timeout 60s` passed again from the actual backend checkout after the container rebuild.
- The complete moderation test executable passed against a dedicated disposable PostgreSQL database: 30 top-level tests and 28 subtests, zero skips/failures. The test database was removed. All media-owner relations, positive flags, kind/ID pairing, duplicates, ACLs, account isolation, rating revisions and shared CAS were covered.
- The existing API/HTML matrix passed 39 content, media and profile-HTML checks; 108 profile privacy checks exposed no other account's email or DOB.
- The optimized user-service Docker image built and reported gRPC SERVING. Source hashes matched the tested production files.
- Existing unrelated backend patches and the separate staged mobile patch were preserved.

After deployment, rerun the saved frontend route workload only with explicit deployment authorization. Compare status, data, warmup, concurrency and request counts before interpreting latency. Local fixtures and local machine timings do not predict production capacity.
