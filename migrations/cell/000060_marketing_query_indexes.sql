BEGIN;

CREATE INDEX marketing_campaigns_all_keyset
ON spyglass.marketing_campaigns(account_id,updated_at DESC,id);

CREATE INDEX marketing_asset_revisions_campaign_keyset
ON spyglass.marketing_asset_revisions(account_id,campaign_id,asset_id,revision DESC);

COMMIT;
