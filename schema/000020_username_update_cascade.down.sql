ALTER TABLE verification_requests
    DROP CONSTRAINT IF EXISTS verification_requests_username_fkey;

ALTER TABLE verification_requests
    ADD CONSTRAINT verification_requests_username_fkey
        FOREIGN KEY (username) REFERENCES user_account (username)
        ON DELETE CASCADE;
