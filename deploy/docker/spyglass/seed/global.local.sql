\set catalog_content `cat /opt/spyglass/catalog/launch-v3.json`

BEGIN;

INSERT INTO cells (id, region, state, assigned_accounts, soft_account_limit, created_at, route_origin)
VALUES
  ('cell-us-east-01', 'us-east', 'active', 0, 1000, statement_timestamp(), :'cell_a_route_origin'),
  ('cell-us-west-01', 'us-west', 'active', 0, 1000, statement_timestamp(), :'cell_b_route_origin')
ON CONFLICT (id) DO UPDATE
SET region = EXCLUDED.region,
    state = EXCLUDED.state,
    soft_account_limit = EXCLUDED.soft_account_limit,
    route_origin = EXCLUDED.route_origin;

INSERT INTO catalog_publications
  (version,state,content,content_hash,created_at,created_by,change_reason)
VALUES
  (3,'draft',:'catalog_content'::jsonb,sha256(convert_to(:'catalog_content','UTF8')),
   statement_timestamp(),'local-catalog-author','publish the approved local launch Catalog')
ON CONFLICT (version) DO NOTHING;

INSERT INTO offer_provider_prices
  (catalog_version,offer_code,provider,mode,provider_price_id,active,created_at)
SELECT 3,mapping.offer_code,'stripe','test',mapping.provider_price_id,true,statement_timestamp()
FROM (VALUES
  ('team-monthly-v2','price_local_team_monthly_v2'),
  ('tokens_10k_v1','price_local_tokens_10k_v1'),
  ('commissioning_v1','price_local_commissioning_v1')
) AS mapping(offer_code,provider_price_id)
WHERE (SELECT state FROM catalog_publications WHERE version=3)='draft'
ON CONFLICT (catalog_version,offer_code,provider,mode) DO NOTHING;

INSERT INTO catalog_operator_events
  (id,catalog_version,action,actor,reason,details,created_at)
VALUES
  ('3a000000-0000-4000-8000-000000000001',3,'draft_created','local-catalog-author','publish the approved local launch Catalog','{}',statement_timestamp()),
  ('3a000000-0000-4000-8000-000000000002',3,'price_mapped','local-catalog-author','map the local recurring Stripe fixture',jsonb_build_object('offer_code','team-monthly-v2','provider','stripe','mode','test','provider_price_id','price_local_team_monthly_v2'),statement_timestamp()),
  ('3a000000-0000-4000-8000-000000000003',3,'price_mapped','local-catalog-author','map the local AI Token Stripe fixture',jsonb_build_object('offer_code','tokens_10k_v1','provider','stripe','mode','test','provider_price_id','price_local_tokens_10k_v1'),statement_timestamp()),
  ('3a000000-0000-4000-8000-000000000004',3,'price_mapped','local-catalog-author','map the local commissioning Stripe fixture',jsonb_build_object('offer_code','commissioning_v1','provider','stripe','mode','test','provider_price_id','price_local_commissioning_v1'),statement_timestamp())
ON CONFLICT (id) DO NOTHING;

DO $local_catalog$
DECLARE
  catalog_state text;
  change_at timestamptz := statement_timestamp();
BEGIN
  SELECT state INTO catalog_state FROM catalog_publications WHERE version=3 FOR UPDATE;
  IF catalog_state='draft' THEN
    UPDATE catalog_publications
    SET state='in_review',review_requested_at=change_at,review_requested_by='local-catalog-author'
    WHERE version=3;
    INSERT INTO catalog_operator_events
      (id,catalog_version,action,actor,reason,details,created_at)
    VALUES
      ('3a000000-0000-4000-8000-000000000005',3,'review_requested','local-catalog-author','request independent local Catalog review','{}',change_at)
    ON CONFLICT (id) DO NOTHING;
    catalog_state := 'in_review';
  END IF;
  IF catalog_state='in_review' THEN
    UPDATE catalog_publications
    SET state='approved',reviewed_at=change_at,reviewed_by='local-catalog-reviewer'
    WHERE version=3;
    INSERT INTO catalog_operator_events
      (id,catalog_version,action,actor,reason,details,created_at)
    VALUES
      ('3a000000-0000-4000-8000-000000000006',3,'approved','local-catalog-reviewer','approve the governed local launch Catalog','{}',change_at)
    ON CONFLICT (id) DO NOTHING;
    catalog_state := 'approved';
  END IF;
  IF catalog_state='approved' THEN
    UPDATE catalog_publications
    SET state='published',published_at=change_at,published_by='local-catalog-publisher'
    WHERE version=3;
    INSERT INTO catalog_operator_events
      (id,catalog_version,action,actor,reason,details,created_at)
    VALUES
      ('3a000000-0000-4000-8000-000000000007',3,'published','local-catalog-publisher','publish the approved local launch Catalog',jsonb_build_object('effective_at',change_at),change_at)
    ON CONFLICT (id) DO NOTHING;
    INSERT INTO entitlement_catalog_rollouts
      (id,target_catalog_version,source,state,effective_at,created_at)
    VALUES
      ('3a000000-0000-4000-8000-000000000008',3,'catalog_publication','pending',change_at,change_at)
    ON CONFLICT DO NOTHING;
  END IF;

  IF EXISTS (SELECT 1 FROM catalog_publications WHERE version=2 AND state='published') THEN
    UPDATE catalog_publications
    SET state='retired',retired_at=change_at,retired_by='local-catalog-publisher'
    WHERE version=2;
    INSERT INTO catalog_operator_events
      (id,catalog_version,action,actor,reason,details,created_at)
    VALUES
      ('3a000000-0000-4000-8000-000000000009',2,'retired','local-catalog-publisher','retire the obsolete local pre-launch Catalog','{}',change_at)
    ON CONFLICT (id) DO NOTHING;
  END IF;
END
$local_catalog$;

COMMIT;
