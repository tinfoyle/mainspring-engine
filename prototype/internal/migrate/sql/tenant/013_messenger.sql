-- The MVP messenger has two tenant-scoped group channels. Support is retained
-- in the tenant so the organization has a durable record; the platform support
-- console can consume this channel through its trusted integration boundary.
CREATE TABLE messenger_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel TEXT NOT NULL CHECK (channel IN ('team', 'support')),
    sender_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    sender_name TEXT NOT NULL,
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX messenger_messages_channel_created_idx
    ON messenger_messages (channel, created_at DESC);
