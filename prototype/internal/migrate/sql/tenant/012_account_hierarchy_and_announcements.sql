-- Tenant identities are local by design. A tenant has exactly one owner once
-- it has any users, and zero or more members. Preserve an existing owner when
-- possible; otherwise promote the earliest legacy administrator (or earliest
-- account) so an upgraded tenant remains administrable.
DO $$
DECLARE
    owner_id UUID;
BEGIN
    SELECT id INTO owner_id
    FROM users
    ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
             created_at, id
    LIMIT 1;

    IF owner_id IS NOT NULL THEN
        UPDATE users
        SET role = CASE WHEN id = owner_id THEN 'owner' ELSE 'member' END
        WHERE role IN ('owner', 'admin', 'member', 'viewer');
    END IF;
END $$;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('owner', 'member'));
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'active'
    CHECK (state IN ('pending', 'active', 'disabled'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS activated_at TIMESTAMPTZ;
UPDATE users SET state = CASE WHEN disabled_at IS NULL THEN 'active' ELSE 'disabled' END;
UPDATE users SET activated_at = COALESCE(activated_at, created_at) WHERE state = 'active';
CREATE UNIQUE INDEX IF NOT EXISTS users_exactly_one_owner_idx ON users ((role)) WHERE role = 'owner';

-- The partial unique index prevents multiple owners. This deferred constraint
-- trigger also prevents an organization with accounts from losing its sole
-- owner while allowing a brand-new tenant to complete its initial setup.
CREATE OR REPLACE FUNCTION enforce_tenant_single_owner() RETURNS TRIGGER AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM users)
       AND (SELECT count(*) FROM users WHERE role = 'owner') <> 1 THEN
        RAISE EXCEPTION 'an organization with users must have exactly one owner';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER users_exactly_one_owner_check
AFTER INSERT OR UPDATE OF role OR DELETE ON users
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_tenant_single_owner();

ALTER TABLE invitations ADD COLUMN IF NOT EXISTS invited_user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE invitations DROP CONSTRAINT IF EXISTS invitations_role_check;
ALTER TABLE invitations ADD CONSTRAINT invitations_role_check CHECK (role = 'member');
UPDATE invitations SET role = 'member';
CREATE UNIQUE INDEX IF NOT EXISTS invitations_pending_email_idx
    ON invitations (email) WHERE accepted_at IS NULL;

CREATE TABLE platform_announcements (
    id UUID PRIMARY KEY,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('planned_downtime', 'service_notice')),
    published_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE platform_announcement_recipients (
    announcement_id UUID NOT NULL REFERENCES platform_announcements(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (announcement_id, user_id)
);
CREATE INDEX platform_announcement_recipients_user_idx
    ON platform_announcement_recipients (user_id, delivered_at DESC);
