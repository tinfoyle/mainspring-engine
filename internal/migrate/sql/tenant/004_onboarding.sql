CREATE TABLE tenant_onboarding (
    tenant_id UUID PRIMARY KEY REFERENCES tenant_settings(tenant_id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'not_started'
        CHECK (status IN ('not_started', 'in_progress', 'completed')),
    current_step INTEGER NOT NULL DEFAULT 1 CHECK (current_step BETWEEN 1 AND 6),
    business_profile JSONB NOT NULL DEFAULT '{}'::jsonb,
    operating_playbook JSONB NOT NULL DEFAULT '{}'::jsonb,
    priorities JSONB NOT NULL DEFAULT '[]'::jsonb,
    boardroom_blueprint JSONB NOT NULL DEFAULT '{}'::jsonb,
    permission_plan JSONB NOT NULL DEFAULT '{}'::jsonb,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Existing tenants were already usable before onboarding existed. Keep them live;
-- development can explicitly reset them into the new flow.
INSERT INTO tenant_onboarding (tenant_id, status, current_step, completed_at)
SELECT tenant_id,
       CASE WHEN EXISTS (SELECT 1 FROM users) THEN 'completed' ELSE 'not_started' END,
       CASE WHEN EXISTS (SELECT 1 FROM users) THEN 6 ELSE 1 END,
       CASE WHEN EXISTS (SELECT 1 FROM users) THEN now() ELSE NULL END
FROM tenant_settings
ON CONFLICT (tenant_id) DO NOTHING;
