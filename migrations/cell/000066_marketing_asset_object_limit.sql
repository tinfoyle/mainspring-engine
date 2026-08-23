BEGIN;

-- Provider payload reconstruction is bounded to 16 MiB. Admission and durable
-- state must share that ceiling so an approved release can always be rebuilt
-- without discovering an oversized legacy object only at execution time.
ALTER TABLE spyglass.marketing_asset_revisions
  ADD CONSTRAINT marketing_asset_revisions_content_bytes_runtime_limit
  CHECK (content_bytes<=16777216) NOT VALID;

ALTER TABLE spyglass.marketing_asset_revisions
  VALIDATE CONSTRAINT marketing_asset_revisions_content_bytes_runtime_limit;

COMMIT;
