-- Global platform identities are deliberately distinct from tenant-local users.
CREATE TABLE platform_administrators (
    account_user_id UUID PRIMARY KEY REFERENCES account_users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'platform_administrator'
        CHECK (role = 'platform_administrator'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE platform_announcements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('planned_downtime', 'service_notice')),
    target TEXT NOT NULL CHECK (target IN ('owners', 'tenant_members', 'all_users')),
    tenant_id UUID REFERENCES tenants(id),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((target = 'all_users' AND tenant_id IS NULL) OR (target IN ('owners', 'tenant_members') AND tenant_id IS NOT NULL))
);
