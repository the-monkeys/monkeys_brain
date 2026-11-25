# User-service moderation ownership implementation plan

**Goal:** Move the existing shared reporting/NSFW RPC from events to users, as requested on 2026-11-25.

**Architecture:** Reuse common/moderation without changing its protobuf or HTTP contracts. Register it on the existing user gRPC server using uDBHandler's primary PostgreSQL pool. Point the gateway's single moderation client at TheMonkeysUser/UserPort. Remove registration from events. No new process, port, pool, queue or migration.

**Constraints:** Work in the actual backend checkout on codex/nsfw-reporting-age-gate. Preserve unrelated changes and staged mobile fixes. Keep ordinary-report pending review, staff restriction transactionality, birthday gating, media protection, legacy adapters and existing APIs unchanged. No production or PR actions.

## Steps
- [x] Add regression tests proving gateway moderation connects to the configured user endpoint and user registration uses the existing SQL pool, with safe errors on an invalid pool.
- [x] Observe failures before moving registration and gateway routing.
- [x] Move the registration helper to users, wire users/main.go, remove events registration and switch the gateway target.
- [x] Update current architecture/deployment documentation: migration first; users then gateway; events last so older gateway instances retain their event-hosted RPC during rollout.
- [x] Run users/events/gateway/storage and shared-policy tests, SQL-backed integration checks on an isolated local test DB, and vet. Review precise diff and preserve unrelated bytes.
Delivery: commit and push the backend follow-up only; no PR. Git history and remote tracking record completion.

## Verification evidence
- RED: gateway endpoint check failed against the old events endpoint; user registration did not exist.
- GREEN: all three new regression tests pass, including four content kinds through the user SQL pool.
- Full affected Go suites: 525 tests/subtests passed, two explicit opt-in tests skipped (browser fixture and Redis integration), 28 packages passed. Real PostgreSQL shared-policy/RPC integration tests executed on the isolated loopback moderation_store_test database.
- go vet passed across users, events, gateway, storage, shared moderation and staff access.
- Independent review found no issues. Original staged mobile patch and unrelated backend working diff remain byte-identical.
