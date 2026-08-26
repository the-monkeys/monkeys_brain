-- Upgrade verification_requests for government-ID based account
-- verification (X / Instagram style): selfie + country-scoped document
-- checksums, one active request per user, review-queue indexing, and a
-- stricter decision lifecycle.
--
-- Companion to the verification RPCs/messages added to
-- apis/serviceconn/gateway_user/pb/gw_user.proto (Phase 0).
--
-- Documents are stored as checksum-addressed assets in a PRIVATE MinIO
-- bucket and registered in storage_assets / storage_asset_refs
-- (schema/000005); this table only holds their SHA-256 fingerprints.

ALTER TABLE verification_requests
    ADD COLUMN country           CHAR(2),     -- ISO 3166-1 alpha-2
    ADD COLUMN id_document_type  VARCHAR(32), -- passport | national_id | drivers_license | residence_permit
    ADD COLUMN selfie_checksum   VARCHAR(64), -- storage_assets.checksum
    ADD COLUMN id_front_checksum VARCHAR(64),
    ADD COLUMN id_back_checksum  VARCHAR(64); -- optional, doc-type dependent

-- At most ONE live request per user. Enforced here rather than in app
-- code so two concurrent submissions cannot both insert.
CREATE UNIQUE INDEX IF NOT EXISTS uq_verification_requests_active
    ON verification_requests (username)
    WHERE status IN ('pending', 'under_review');

-- Admin review queue scan: filter by status, newest first.
CREATE INDEX IF NOT EXISTS idx_verification_requests_queue
    ON verification_requests (status, created_at DESC);

-- The new id_document flow carries documents as checksums, not URLs.
-- Legacy rows (social_proof / professional) keep their comma-separated
-- proof_urls; new id_document rows simply leave it NULL.
ALTER TABLE verification_requests
    ALTER COLUMN proof_urls DROP NOT NULL;

-- Document completeness rules:
--   * id_document      -> selfie + ID front + 2-letter country + doc type
--   * legacy types     -> tolerated unchanged (pre-migration rows)
ALTER TABLE verification_requests
    ADD CONSTRAINT chk_verification_id_document CHECK (
        verification_type <> 'id_document'
        OR (
            selfie_checksum       IS NOT NULL
            AND id_front_checksum IS NOT NULL
            AND country           IS NOT NULL AND char_length(trim(country)) = 2
            AND id_document_type  IS NOT NULL AND char_length(trim(id_document_type)) > 0
        )
    ),
    ADD CONSTRAINT chk_verification_selfie CHECK (
        verification_type IN ('social_proof', 'professional') -- legacy paths exempt
        OR selfie_checksum IS NOT NULL
    );

-- Referential integrity for document assets (checksum-addressed CAS).
ALTER TABLE verification_requests
    ADD CONSTRAINT fk_verification_selfie  FOREIGN KEY (selfie_checksum)  REFERENCES storage_assets (checksum),
    ADD CONSTRAINT fk_verification_idfront FOREIGN KEY (id_front_checksum) REFERENCES storage_assets (checksum),
    ADD CONSTRAINT fk_verification_idback  FOREIGN KEY (id_back_checksum)  REFERENCES storage_assets (checksum);

-- Stricter lifecycle: inserts 'under_review' between pending and the
-- terminal states. Existing rows (pending/approved/rejected) satisfy it
-- unchanged.
ALTER TABLE verification_requests
    ADD CONSTRAINT chk_verification_status
        CHECK (status IN ('pending', 'under_review', 'approved', 'rejected'));
