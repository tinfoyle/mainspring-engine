BEGIN;

CREATE TABLE offer_provider_prices (
    catalog_version bigint NOT NULL REFERENCES catalog_publications (version),
    offer_code text NOT NULL,
    provider text NOT NULL CHECK (provider = 'stripe'),
    mode text NOT NULL CHECK (mode IN ('test','live')),
    provider_price_id text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (catalog_version, offer_code, provider, mode),
    UNIQUE (provider, mode, provider_price_id)
);

ALTER TABLE subscriptions ADD COLUMN provider_customer_id text;
ALTER TABLE subscriptions ADD COLUMN provider_mode text NOT NULL DEFAULT 'test' CHECK (provider_mode IN ('test','live'));
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_provider_subscription_id_key;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_provider_mode_identity UNIQUE (provider,provider_mode,provider_subscription_id);
CREATE INDEX subscriptions_provider_customer ON subscriptions (provider,provider_mode,provider_customer_id);
CREATE UNIQUE INDEX entitlement_grants_subscription_package
    ON entitlement_grants (account_id, source_reference, package_code)
    WHERE source = 'subscription';

CREATE TABLE billing_reconciliation_queue (
    provider_subscription_id text PRIMARY KEY,
    reason text NOT NULL,
    requested_at timestamptz NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    last_error_code text,
    processing_state text NOT NULL DEFAULT 'pending' CHECK (processing_state IN ('pending','processing','failed','completed')),
    lease_expires_at timestamptz,
    completed_at timestamptz
);
CREATE INDEX billing_reconciliation_pending ON billing_reconciliation_queue (next_attempt_at) WHERE processing_state IN ('pending','failed');

WITH catalog_payload(content) AS (
    VALUES ($catalog$
    {
      "version": 2,
      "packages": [
        {"code":"knowledge","version":1,"name":"Knowledge","description":"Source-attributed business facts, documents, evidence, and citations.","features":["knowledge.read","knowledge.baseline"],"default_limits":{"documents":25}},
        {"code":"work","version":1,"name":"Work","description":"Accountable work across people and agents.","features":["work.read","work.manage"],"default_limits":{"active_items":100}},
        {"code":"agents","version":1,"name":"Agents","description":"Governed specialist agents and coordinated boardrooms.","dependencies":["work","knowledge"],"features":["agents.configure","agents.run"],"default_limits":{"concurrent_runs":2}},
        {"code":"finance","version":1,"name":"Finance","description":"Operational ledgers, accounts, entries, and reports.","features":["finance.read","finance.post"]},
        {"code":"marketing","version":1,"name":"Marketing","description":"Brand knowledge, research, campaign planning, and content work.","dependencies":["knowledge"],"features":["marketing.read","marketing.manage"]},
        {"code":"integrations","version":1,"name":"Integrations","description":"Scoped, observable external connectors.","features":["integrations.read","integrations.connect"]}
      ],
      "plans": [
        {"code":"free","version":1,"name":"Free","description":"A real Spyglass Account for exploring the operating model.","packages":{"knowledge":"enabled"}},
        {"code":"team","version":1,"name":"Team","description":"A focused operating surface for a growing team.","packages":{"knowledge":"enabled","work":"enabled","integrations":"enabled"}},
        {"code":"operating","version":1,"name":"Operating","description":"The coordinated Spyglass operating system.","packages":{"knowledge":"enabled","work":"enabled","agents":"enabled","finance":"enabled","marketing":"enabled","integrations":"enabled"}}
      ],
      "offers": [
        {"code":"free-v1","plan_code":"free","plan_version":1,"currency":"USD","amount_minor":0,"billing_interval":"none"},
        {"code":"team-monthly-v1","plan_code":"team","plan_version":1,"currency":"USD","amount_minor":4900,"billing_interval":"month"},
        {"code":"operating-monthly-v1","plan_code":"operating","plan_version":1,"currency":"USD","amount_minor":14900,"billing_interval":"month"}
      ]
    }
    $catalog$))
INSERT INTO catalog_publications (version,state,published_at,content,content_hash,created_at)
SELECT 2,'published',statement_timestamp(),content::jsonb,sha256(convert_to(content,'UTF8')),statement_timestamp()
FROM catalog_payload;

UPDATE catalog_publications SET state='retired' WHERE version=1 AND state='published';

COMMIT;
