-- Revert 000013: restore verification_requests to its 000004 shape.
-- Order matters: constraints/indexes first, columns last.

ALTER TABLE verification_requests DROP CONSTRAINT IF EXISTS chk_verification_status;

ALTER TABLE verification_requests
    DROP CONSTRAINT IF EXISTS fk_verification_selfie,
    DROP CONSTRAINT IF EXISTS fk_verification_idfront,
    DROP CONSTRAINT IF EXISTS fk_verification_idback;

ALTER TABLE verification_requests
    DROP CONSTRAINT IF EXISTS chk_verification_id_document,
    DROP CONSTRAINT IF EXISTS chk_verification_selfie;

DROP INDEX IF EXISTS idx_verification_requests_queue;
DROP INDEX IF EXISTS uq_verification_requests_active;

-- 000013 made proof_urls nullable (checksum-based flow). Restore NOT NULL
-- safely: backfill any new-era NULL rows before re-applying the guard.
UPDATE verification_requests SET proof_urls = '' WHERE proof_urls IS NULL;

ALTER TABLE verification_requests
    ALTER COLUMN proof_urls SET NOT NULL;

ALTER TABLE verification_requests
    DROP COLUMN IF EXISTS id_back_checksum,
    DROP COLUMN IF EXISTS id_front_checksum,
    DROP COLUMN IF EXISTS selfie_checksum,
    DROP COLUMN IF EXISTS id_document_type,
    DROP COLUMN IF EXISTS country;
