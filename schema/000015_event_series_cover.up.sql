-- Canonical cover URL for a recurring series. Occurrences store the same
-- path string (one MinIO object); they do not each get a copied blob.
ALTER TABLE event_series ADD COLUMN IF NOT EXISTS cover_image TEXT;
