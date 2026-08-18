BEGIN;

WITH catalog_payload(content) AS (
    VALUES ($catalog$
    {
      "version": 1,
      "packages": [
        {"code":"knowledge","version":1,"name":"Knowledge","description":"Source-attributed business facts, documents, evidence, and citations.","features":["knowledge.read","knowledge.baseline"],"default_limits":{"documents":25}},
        {"code":"work","version":1,"name":"Work","description":"Accountable work across people and agents.","features":["work.read","work.manage"],"default_limits":{"active_items":100}},
        {"code":"agents","version":1,"name":"Agents","description":"Governed specialist agents and coordinated boardrooms.","dependencies":["work","knowledge"],"features":["agents.configure","agents.run"],"default_limits":{"concurrent_runs":2}},
        {"code":"finance","version":1,"name":"Finance","description":"Operational ledgers, accounts, entries, and reports.","features":["finance.read","finance.post"]},
        {"code":"marketing","version":1,"name":"Marketing","description":"Brand knowledge, research, campaign planning, and content work.","dependencies":["knowledge"],"features":["marketing.read","marketing.manage"]},
        {"code":"integrations","version":1,"name":"Integrations","description":"Scoped, observable external connectors.","features":["integrations.read","integrations.connect"]}
      ],
      "plans": [
        {"code":"free","version":1,"name":"Free","description":"A real Spyglass Account for exploring the operating model.","packages":{"knowledge":"enabled"}}
      ],
      "offers": [
        {"code":"free-v1","plan_code":"free","plan_version":1,"currency":"USD","amount_minor":0,"billing_interval":"none"}
      ]
    }
    $catalog$))
INSERT INTO catalog_publications (version, state, published_at, content, content_hash, created_at)
SELECT 1, 'published', statement_timestamp(), content::jsonb, sha256(convert_to(content, 'UTF8')), statement_timestamp()
FROM catalog_payload
ON CONFLICT (version) DO NOTHING;

COMMIT;
