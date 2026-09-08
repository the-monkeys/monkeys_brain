-- verification_requests.username FKs user_account.username with ON DELETE
-- CASCADE only. Renaming a verified (or once-verified) account then fails:
--   ERROR: update or delete on table "user_account" violates foreign key
--   constraint "verification_requests_username_fkey" (SQLSTATE 23503)
-- which the gateway maps to HTTP 500. Cascade the username so the row stays
-- attached to the same person.

ALTER TABLE verification_requests
    DROP CONSTRAINT IF EXISTS verification_requests_username_fkey;

ALTER TABLE verification_requests
    ADD CONSTRAINT verification_requests_username_fkey
        FOREIGN KEY (username) REFERENCES user_account (username)
        ON UPDATE CASCADE
        ON DELETE CASCADE;
