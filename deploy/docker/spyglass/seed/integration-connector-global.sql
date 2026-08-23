\set ON_ERROR_STOP on
BEGIN;

DELETE FROM entitlement_snapshots WHERE account_id='82100000-0000-4000-8000-000000000001';
DELETE FROM account_directory WHERE account_id='82100000-0000-4000-8000-000000000001';
DELETE FROM accounts WHERE id='82100000-0000-4000-8000-000000000001';
DELETE FROM users WHERE id='82300000-0000-4000-8000-000000000003';

INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at)
VALUES ('82300000-0000-4000-8000-000000000003','connector-cert@infiniteocean.localhost','Connector Certification','active',transaction_timestamp(),1,transaction_timestamp());

INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at)
SELECT '82100000-0000-4000-8000-000000000001','connector-certification','Connector Certification','free','active','cell-us-east-01',1,1,version,1,
  '82300000-0000-4000-8000-000000000003',transaction_timestamp()
FROM catalog_publications WHERE state='published' ORDER BY version DESC LIMIT 1;

INSERT INTO entitlement_snapshots(account_id,version,catalog_version,evaluated_at,source_hash,effective_packages)
SELECT '82100000-0000-4000-8000-000000000001',1,version,transaction_timestamp(),decode(repeat('91',32),'hex'),
  '[{"code":"marketing","mode":"enabled","limits":{},"sources":["support_override"],"version":1,"limit_policies":{}},{"code":"integrations","mode":"enabled","limits":{},"sources":["support_override"],"version":1,"limit_policies":{}}]'::jsonb
FROM catalog_publications WHERE state='published' ORDER BY version DESC LIMIT 1;

INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','cell-us-east-01',1,'active','us-east',transaction_timestamp());

COMMIT;
