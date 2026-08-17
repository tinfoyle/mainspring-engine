BEGIN;

WITH catalog_payload(content) AS (
    VALUES ($catalog$
    {
      "version": 1,
      "packages": [
        {"code":"knowledge","version":1,"name":"Knowledge","features":["knowledge.read","knowledge.baseline"],"default_limits":{"documents":25}},
        {"code":"work","version":1,"name":"Work","features":["work.read","work.manage"],"default_limits":{"active_items":100}},
        {"code":"agents","version":1,"name":"Agents","dependencies":["work","knowledge"],"features":["agents.configure","agents.run"],"default_limits":{"concurrent_runs":2}},
        {"code":"finance","version":1,"name":"Finance","features":["finance.read","finance.post"]},
        {"code":"marketing","version":1,"name":"Marketing","dependencies":["knowledge"],"features":["marketing.read","marketing.manage"]},
        {"code":"integrations","version":1,"name":"Integrations","features":["integrations.read","integrations.connect"]}
      ],
      "plans": [
        {"code":"free","version":1,"name":"Free","packages":{"knowledge":"enabled"}}
      ],
      "offers": [
        {"code":"free-v1","plan_code":"free","plan_version":1,"currency":"USD","amount_minor":0,"billing_interval":"none"}
      ]
    }
    $catalog$)
INSERT INTO catalog_publications (version, state, published_at, content, content_hash, created_at)
SELECT 1, 'published', statement_timestamp(), content::jsonb, sha256(convert_to(content, 'UTF8')), statement_timestamp()
FROM catalog_payload
ON CONFLICT (version) DO NOTHING;

COMMIT;
