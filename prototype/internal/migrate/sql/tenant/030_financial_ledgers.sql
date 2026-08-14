CREATE TABLE financial_ledgers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 160),
    code TEXT NOT NULL CHECK (length(btrim(code)) BETWEEN 1 AND 40),
    description TEXT NOT NULL DEFAULT '',
    currency CHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('user', 'agent', 'mcp', 'system')),
    created_by_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (code)
);

CREATE TABLE financial_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ledger_id UUID NOT NULL REFERENCES financial_ledgers(id) ON DELETE CASCADE,
    parent_account_id UUID REFERENCES financial_accounts(id),
    code TEXT NOT NULL CHECK (length(btrim(code)) BETWEEN 1 AND 40),
    name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '',
    account_type TEXT NOT NULL CHECK (account_type IN ('asset', 'liability', 'equity', 'income', 'expense')),
    normal_balance TEXT NOT NULL CHECK (normal_balance IN ('debit', 'credit')),
    allow_posting BOOLEAN NOT NULL DEFAULT true,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('user', 'agent', 'mcp', 'system')),
    created_by_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (ledger_id, code),
    CHECK (parent_account_id IS NULL OR parent_account_id <> id)
);

CREATE INDEX financial_accounts_ledger_parent_idx ON financial_accounts(ledger_id, parent_account_id, code);

CREATE SEQUENCE financial_entry_number_seq;

CREATE TABLE financial_journal_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ledger_id UUID NOT NULL REFERENCES financial_ledgers(id) ON DELETE RESTRICT,
    entry_number BIGINT NOT NULL DEFAULT nextval('financial_entry_number_seq'),
    entry_date DATE NOT NULL,
    description TEXT NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 500),
    reference TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'voided')),
    reversal_of_id UUID REFERENCES financial_journal_entries(id),
    source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'agent', 'mcp', 'import', 'system', 'reversal')),
    work_item_id UUID REFERENCES work_items(id) ON DELETE SET NULL,
    run_id UUID REFERENCES boardroom_runs(id) ON DELETE SET NULL,
    invocation_id UUID,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('user', 'agent', 'mcp', 'system')),
    created_by_id TEXT NOT NULL,
    posted_by_type TEXT,
    posted_by_id TEXT,
    posted_at TIMESTAMPTZ,
    voided_by_type TEXT,
    voided_by_id TEXT,
    voided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entry_number)
);

CREATE INDEX financial_entries_ledger_date_idx ON financial_journal_entries(ledger_id, entry_date DESC, entry_number DESC);
CREATE INDEX financial_entries_work_item_idx ON financial_journal_entries(work_item_id) WHERE work_item_id IS NOT NULL;

CREATE TABLE financial_journal_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    journal_entry_id UUID NOT NULL REFERENCES financial_journal_entries(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES financial_accounts(id) ON DELETE RESTRICT,
    line_number INTEGER NOT NULL CHECK (line_number > 0),
    memo TEXT NOT NULL DEFAULT '',
    debit_minor BIGINT NOT NULL DEFAULT 0 CHECK (debit_minor >= 0),
    credit_minor BIGINT NOT NULL DEFAULT 0 CHECK (credit_minor >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (journal_entry_id, line_number),
    CHECK ((debit_minor > 0 AND credit_minor = 0) OR (credit_minor > 0 AND debit_minor = 0))
);

CREATE INDEX financial_lines_account_idx ON financial_journal_lines(account_id, journal_entry_id);

CREATE TABLE financial_ledger_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ledger_id UUID NOT NULL REFERENCES financial_ledgers(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    action TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'agent', 'mcp', 'system')),
    actor_id TEXT NOT NULL,
    before_payload JSONB,
    after_payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX financial_events_ledger_created_idx ON financial_ledger_events(ledger_id, created_at DESC);

-- Every agent can inspect and maintain the books. Mutations remain attributable
-- through the append-only financial event log and the general tool-call audit.
INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT p.id, capability, '{}'::jsonb
FROM personas p
CROSS JOIN (VALUES ('finance.read'), ('finance.manage')) AS finance(capability)
ON CONFLICT (persona_id, capability) DO NOTHING;
