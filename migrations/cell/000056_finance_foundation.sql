BEGIN;

CREATE TABLE spyglass.finance_ledgers (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160 AND name=btrim(name)),
    code text NOT NULL CHECK (char_length(code) BETWEEN 1 AND 40 AND code=upper(btrim(code))),
    description text NOT NULL CHECK (octet_length(description)<=4000 AND description=btrim(description)),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    state text NOT NULL CHECK (state IN ('active','archived')),
    closed_through date,
    version bigint NOT NULL CHECK (version>0),
    created_by_kind text NOT NULL CHECK (created_by_kind='user'),
    created_by_id text NOT NULL CHECK (char_length(created_by_id)=36),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,code),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE TABLE spyglass.finance_ledger_close_evidence (
    account_id uuid NOT NULL,
    ledger_id uuid NOT NULL,
    ledger_version bigint NOT NULL CHECK (ledger_version>1),
    evidence_id uuid NOT NULL,
    closed_through date NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,ledger_id,ledger_version,evidence_id),
    FOREIGN KEY (account_id,ledger_id) REFERENCES spyglass.finance_ledgers(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,evidence_id) REFERENCES spyglass.knowledge_evidence(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.finance_accounts (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    ledger_id uuid NOT NULL,
    parent_account_id uuid,
    code text NOT NULL CHECK (char_length(code) BETWEEN 1 AND 40 AND code=upper(btrim(code))),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160 AND name=btrim(name)),
    description text NOT NULL CHECK (octet_length(description)<=4000 AND description=btrim(description)),
    account_type text NOT NULL CHECK (account_type IN ('asset','liability','equity','income','expense')),
    normal_balance text NOT NULL CHECK (normal_balance IN ('debit','credit')),
    allow_posting boolean NOT NULL,
    state text NOT NULL CHECK (state IN ('active','archived')),
    version bigint NOT NULL CHECK (version>0),
    created_by_kind text NOT NULL CHECK (created_by_kind='user'),
    created_by_id text NOT NULL CHECK (char_length(created_by_id)=36),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,ledger_id,id),
    UNIQUE (account_id,ledger_id,code),
    FOREIGN KEY (account_id,ledger_id) REFERENCES spyglass.finance_ledgers(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,ledger_id,parent_account_id) REFERENCES spyglass.finance_accounts(account_id,ledger_id,id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CHECK (parent_account_id IS NULL OR parent_account_id<>id),
    CHECK ((account_type IN ('asset','expense') AND normal_balance='debit') OR
           (account_type IN ('liability','equity','income') AND normal_balance='credit')),
    CHECK (state='active' OR NOT allow_posting)
);

CREATE TABLE spyglass.finance_entry_number_counters (
    account_id uuid NOT NULL,
    ledger_id uuid NOT NULL,
    next_number bigint NOT NULL CHECK (next_number>0),
    PRIMARY KEY (account_id,ledger_id),
    FOREIGN KEY (account_id,ledger_id) REFERENCES spyglass.finance_ledgers(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.finance_entries (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    ledger_id uuid NOT NULL,
    entry_number bigint NOT NULL CHECK (entry_number>0),
    entry_date date NOT NULL,
    description text NOT NULL CHECK (octet_length(description) BETWEEN 1 AND 4000 AND description=btrim(description)),
    reference text NOT NULL CHECK (octet_length(reference)<=500 AND reference=btrim(reference)),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    total_minor bigint NOT NULL CHECK (total_minor>0),
    source text NOT NULL CHECK (source IN ('manual','agent','mcp','import','system','reversal')),
    work_item_id uuid,
    run_id uuid,
    invocation_id uuid,
    state text NOT NULL CHECK (state IN ('draft','posted','reversed')),
    reversal_of_id uuid,
    reversed_by_id uuid,
    version bigint NOT NULL CHECK (version>0),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 200 AND created_by_id=btrim(created_by_id)),
    posted_by_user_id uuid,
    reversed_by_user_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    posted_at timestamptz,
    reversed_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,ledger_id,entry_number),
    FOREIGN KEY (account_id,ledger_id) REFERENCES spyglass.finance_ledgers(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id,run_id) REFERENCES spyglass.agent_runs(account_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id,invocation_id) REFERENCES spyglass.agent_invocations(account_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id,reversal_of_id) REFERENCES spyglass.finance_entries(account_id,id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (account_id,reversed_by_id) REFERENCES spyglass.finance_entries(account_id,id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CHECK ((source='agent' AND created_by_kind='workload' AND run_id IS NOT NULL AND invocation_id IS NOT NULL) OR (source<>'agent' AND created_by_kind='user')),
    CHECK ((source='reversal' AND created_by_kind='user' AND reversal_of_id IS NOT NULL) OR (source<>'reversal' AND reversal_of_id IS NULL)),
    CHECK ((state='draft' AND posted_by_user_id IS NULL AND reversed_by_user_id IS NULL AND posted_at IS NULL AND reversed_at IS NULL AND reversed_by_id IS NULL) OR
           (state='posted' AND posted_by_user_id IS NOT NULL AND reversed_by_user_id IS NULL AND posted_at>=created_at AND reversed_at IS NULL AND reversed_by_id IS NULL) OR
           (state='reversed' AND source<>'reversal' AND posted_by_user_id IS NOT NULL AND reversed_by_user_id IS NOT NULL AND posted_at>=created_at AND reversed_at>=posted_at AND reversed_by_id IS NOT NULL))
);

CREATE TABLE spyglass.finance_entry_lines (
    account_id uuid NOT NULL,
    entry_id uuid NOT NULL,
    line_number smallint NOT NULL CHECK (line_number BETWEEN 1 AND 100),
    ledger_id uuid NOT NULL,
    posting_account_id uuid NOT NULL,
    memo text NOT NULL CHECK (octet_length(memo)<=1000 AND memo=btrim(memo)),
    debit_minor bigint NOT NULL CHECK (debit_minor>=0),
    credit_minor bigint NOT NULL CHECK (credit_minor>=0),
    PRIMARY KEY (account_id,entry_id,line_number),
    FOREIGN KEY (account_id,entry_id) REFERENCES spyglass.finance_entries(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,ledger_id,posting_account_id) REFERENCES spyglass.finance_accounts(account_id,ledger_id,id) ON DELETE CASCADE,
    CHECK ((debit_minor>0 AND credit_minor=0) OR (credit_minor>0 AND debit_minor=0))
);

CREATE TABLE spyglass.finance_entry_evidence (
    account_id uuid NOT NULL,
    entry_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    PRIMARY KEY (account_id,entry_id,evidence_id),
    FOREIGN KEY (account_id,entry_id) REFERENCES spyglass.finance_entries(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,evidence_id) REFERENCES spyglass.knowledge_evidence(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.finance_reconciliations (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    ledger_id uuid NOT NULL,
    posting_account_id uuid NOT NULL,
    as_of date NOT NULL,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    statement_balance_minor bigint NOT NULL,
    ledger_balance_minor bigint NOT NULL,
    difference_minor bigint NOT NULL,
    primary_evidence_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('proposed','discrepancy','confirmed')),
    version bigint NOT NULL CHECK (version>0),
    created_by_user_id uuid NOT NULL,
    confirmed_by_user_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    confirmed_at timestamptz,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,ledger_id) REFERENCES spyglass.finance_ledgers(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,ledger_id,posting_account_id) REFERENCES spyglass.finance_accounts(account_id,ledger_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,primary_evidence_id) REFERENCES spyglass.knowledge_evidence(account_id,id) ON DELETE CASCADE,
    CHECK (difference_minor::numeric=statement_balance_minor::numeric-ledger_balance_minor::numeric),
    CHECK ((state='proposed' AND difference_minor=0 AND confirmed_by_user_id IS NULL AND confirmed_at IS NULL) OR
           (state='discrepancy' AND difference_minor<>0 AND confirmed_by_user_id IS NULL AND confirmed_at IS NULL) OR
           (state='confirmed' AND difference_minor=0 AND confirmed_by_user_id IS NOT NULL AND confirmed_at>=created_at))
);

CREATE TABLE spyglass.finance_reconciliation_evidence (
    account_id uuid NOT NULL,
    reconciliation_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    PRIMARY KEY (account_id,reconciliation_id,evidence_id),
    FOREIGN KEY (account_id,reconciliation_id) REFERENCES spyglass.finance_reconciliations(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,evidence_id) REFERENCES spyglass.knowledge_evidence(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.finance_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    aggregate_kind text NOT NULL CHECK (aggregate_kind IN ('ledger','account','entry','reconciliation')),
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('created','revised','archived','period_closed','posted','reversed','reconciliation_proposed','reconciliation_discrepancy','reconciliation_confirmed')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200 AND actor_id=btrim(actor_id)),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=4096 AND
        NOT (redacted_payload ?| ARRAY['name','description','memo','reference','source_reference','canonical_value'])),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE INDEX finance_ledgers_list ON spyglass.finance_ledgers(account_id,state,code,id);
CREATE INDEX finance_accounts_tree ON spyglass.finance_accounts(account_id,ledger_id,parent_account_id,code,id);
CREATE INDEX finance_entries_journal ON spyglass.finance_entries(account_id,ledger_id,entry_date DESC,entry_number DESC);
CREATE INDEX finance_entries_work ON spyglass.finance_entries(account_id,work_item_id) WHERE work_item_id IS NOT NULL;
CREATE UNIQUE INDEX finance_entries_one_reversal ON spyglass.finance_entries(account_id,reversal_of_id) WHERE reversal_of_id IS NOT NULL;
CREATE UNIQUE INDEX finance_entries_one_reverse_link ON spyglass.finance_entries(account_id,reversed_by_id) WHERE reversed_by_id IS NOT NULL;
CREATE INDEX finance_reconciliations_list ON spyglass.finance_reconciliations(account_id,ledger_id,as_of DESC,id);
CREATE UNIQUE INDEX finance_one_confirmed_reconciliation
    ON spyglass.finance_reconciliations(account_id,ledger_id,posting_account_id,as_of) WHERE state='confirmed';
CREATE INDEX finance_events_aggregate ON spyglass.finance_events(account_id,aggregate_kind,aggregate_id,occurred_at,id);

ALTER TABLE spyglass.finance_ledgers ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_ledgers FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_ledger_close_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_ledger_close_evidence FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_accounts FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entry_number_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entry_number_counters FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entries FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entry_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entry_lines FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entry_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_entry_evidence FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_reconciliations ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_reconciliations FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_reconciliation_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_reconciliation_evidence FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.finance_events FORCE ROW LEVEL SECURITY;

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['finance_ledgers','finance_ledger_close_evidence','finance_accounts','finance_entry_number_counters','finance_entries','finance_entry_lines','finance_entry_evidence','finance_reconciliations','finance_reconciliation_evidence','finance_events'] LOOP
        EXECUTE format('CREATE POLICY %I ON spyglass.%I USING (account_id=nullif(current_setting(''app.account_id'',true),'''')::uuid) WITH CHECK (account_id=nullif(current_setting(''app.account_id'',true),'''')::uuid)',table_name||'_isolation',table_name);
    END LOOP;
END $$;

CREATE FUNCTION spyglass.protect_finance_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Finance identity is immutable';
    END IF;
    IF TG_TABLE_NAME='finance_ledgers' THEN
        IF NEW.currency IS DISTINCT FROM OLD.currency OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR NEW.created_by_id IS DISTINCT FROM OLD.created_by_id THEN
            RAISE EXCEPTION 'Finance Ledger identity is immutable';
        END IF;
    ELSIF TG_TABLE_NAME='finance_accounts' THEN
        IF NEW.ledger_id IS DISTINCT FROM OLD.ledger_id OR NEW.account_type IS DISTINCT FROM OLD.account_type OR NEW.normal_balance IS DISTINCT FROM OLD.normal_balance OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR NEW.created_by_id IS DISTINCT FROM OLD.created_by_id THEN
            RAISE EXCEPTION 'Finance Account identity is immutable';
        END IF;
    END IF;
    IF NEW.version<>OLD.version+1 THEN RAISE EXCEPTION 'Finance version transition is invalid'; END IF;
    IF TG_TABLE_NAME='finance_ledgers' THEN
        IF NEW.closed_through IS DISTINCT FROM OLD.closed_through THEN
            IF NEW.closed_through IS NULL OR (OLD.closed_through IS NOT NULL AND NEW.closed_through<=OLD.closed_through) OR NOT EXISTS (
                SELECT 1 FROM spyglass.finance_ledger_close_evidence e WHERE e.account_id=NEW.account_id AND e.ledger_id=NEW.id AND e.ledger_version=NEW.version AND e.closed_through=NEW.closed_through
            ) THEN RAISE EXCEPTION 'Finance period close evidence is invalid'; END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_finance_entry() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ledger_row record; original_row record; line_count integer; debit_total numeric; credit_total numeric; evidence_count integer; unavailable_count integer;
BEGIN
    SELECT state,currency,closed_through INTO ledger_row FROM spyglass.finance_ledgers WHERE account_id=NEW.account_id AND id=NEW.ledger_id;
    IF NOT FOUND OR ledger_row.currency<>NEW.currency THEN RAISE EXCEPTION 'Finance Ledger currency is invalid'; END IF;
    IF TG_OP='INSERT' THEN
        IF NEW.state<>'draft' OR ledger_row.state<>'active' THEN RAISE EXCEPTION 'Finance entry must begin as a draft in an active Ledger'; END IF;
        IF ledger_row.closed_through IS NOT NULL AND NEW.entry_date<=ledger_row.closed_through THEN RAISE EXCEPTION 'Finance period is closed'; END IF;
        IF NEW.source='reversal' THEN
            SELECT state,ledger_id INTO original_row FROM spyglass.finance_entries WHERE account_id=NEW.account_id AND id=NEW.reversal_of_id;
            IF NOT FOUND OR original_row.state<>'posted' OR original_row.ledger_id<>NEW.ledger_id THEN RAISE EXCEPTION 'Finance reversal target is invalid'; END IF;
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.state='draft' AND NEW.state='draft' AND ledger_row.closed_through IS NOT NULL AND NEW.entry_date<=ledger_row.closed_through THEN
        RAISE EXCEPTION 'Finance period is closed';
    END IF;
    IF OLD.state='draft' AND NEW.state='posted' THEN
        IF ledger_row.state<>'active' OR (ledger_row.closed_through IS NOT NULL AND NEW.entry_date<=ledger_row.closed_through) THEN RAISE EXCEPTION 'Finance period is closed'; END IF;
        SELECT count(*),COALESCE(sum(debit_minor::numeric),0),COALESCE(sum(credit_minor::numeric),0),
          count(*) FILTER (WHERE a.state<>'active' OR NOT a.allow_posting)
          INTO line_count,debit_total,credit_total,unavailable_count
          FROM spyglass.finance_entry_lines l JOIN spyglass.finance_accounts a
            ON a.account_id=l.account_id AND a.ledger_id=l.ledger_id AND a.id=l.posting_account_id
          WHERE l.account_id=NEW.account_id AND l.entry_id=NEW.id;
        SELECT count(*) INTO evidence_count FROM spyglass.finance_entry_evidence WHERE account_id=NEW.account_id AND entry_id=NEW.id;
        IF line_count NOT BETWEEN 2 AND 100 OR debit_total<=0 OR debit_total<>credit_total OR debit_total<>NEW.total_minor::numeric OR unavailable_count<>0 OR evidence_count=0 THEN
            RAISE EXCEPTION 'Finance posting is not balanced, evidenced, or available';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_finance_entry() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
        IF OLD.state<>'draft' THEN RAISE EXCEPTION 'Posted Finance entries are immutable'; END IF;
        RETURN OLD;
    END IF;
    IF NEW.version<>OLD.version+1 THEN RAISE EXCEPTION 'Finance version transition is invalid'; END IF;
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.ledger_id IS DISTINCT FROM OLD.ledger_id OR
       NEW.entry_number IS DISTINCT FROM OLD.entry_number OR NEW.currency IS DISTINCT FROM OLD.currency OR NEW.source IS DISTINCT FROM OLD.source OR
       NEW.work_item_id IS DISTINCT FROM OLD.work_item_id OR NEW.run_id IS DISTINCT FROM OLD.run_id OR NEW.invocation_id IS DISTINCT FROM OLD.invocation_id OR
       NEW.reversal_of_id IS DISTINCT FROM OLD.reversal_of_id OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR NEW.created_by_id IS DISTINCT FROM OLD.created_by_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at THEN RAISE EXCEPTION 'Finance entry identity is immutable'; END IF;
    IF OLD.state='reversed' OR (OLD.state='posted' AND NEW.state<>'reversed') THEN RAISE EXCEPTION 'Posted Finance entries are immutable'; END IF;
    IF OLD.state='posted' AND (NEW.entry_date IS DISTINCT FROM OLD.entry_date OR NEW.description IS DISTINCT FROM OLD.description OR NEW.reference IS DISTINCT FROM OLD.reference OR
       NEW.total_minor IS DISTINCT FROM OLD.total_minor OR NEW.version<>OLD.version+1 OR NEW.posted_by_user_id IS DISTINCT FROM OLD.posted_by_user_id OR NEW.posted_at IS DISTINCT FROM OLD.posted_at OR
       NEW.reversed_by_id IS NULL OR NEW.reversed_by_user_id IS NULL OR NEW.reversed_at IS NULL) THEN RAISE EXCEPTION 'Finance reversal transition is invalid'; END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_finance_parent_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Finance aggregate deletion is prohibited';
END;
$$;

CREATE FUNCTION spyglass.protect_finance_entry_child() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_account uuid; row_entry uuid; entry_state text;
BEGIN
    IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
    row_account:=CASE WHEN TG_OP='DELETE' THEN OLD.account_id ELSE NEW.account_id END;
    row_entry:=CASE WHEN TG_OP='DELETE' THEN OLD.entry_id ELSE NEW.entry_id END;
    SELECT state INTO entry_state FROM spyglass.finance_entries WHERE account_id=row_account AND id=row_entry;
    IF entry_state<>'draft' THEN RAISE EXCEPTION 'Posted Finance entry children are immutable'; END IF;
    RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE FUNCTION spyglass.reject_finance_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND (pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE'))) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Finance evidence and events are immutable';
END;
$$;

CREATE FUNCTION spyglass.protect_finance_reconciliation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND (pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE'))) THEN RETURN OLD; END IF;
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'Finance reconciliations are immutable'; END IF;
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.ledger_id IS DISTINCT FROM OLD.ledger_id OR NEW.posting_account_id IS DISTINCT FROM OLD.posting_account_id OR
       NEW.as_of IS DISTINCT FROM OLD.as_of OR NEW.currency IS DISTINCT FROM OLD.currency OR NEW.statement_balance_minor IS DISTINCT FROM OLD.statement_balance_minor OR
       NEW.ledger_balance_minor IS DISTINCT FROM OLD.ledger_balance_minor OR NEW.difference_minor IS DISTINCT FROM OLD.difference_minor OR NEW.primary_evidence_id IS DISTINCT FROM OLD.primary_evidence_id OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.state<>'proposed' OR NEW.state<>'confirmed' OR NEW.version<>OLD.version+1 THEN
        RAISE EXCEPTION 'Finance reconciliation is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER finance_ledgers_identity BEFORE UPDATE ON spyglass.finance_ledgers FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_identity();
CREATE TRIGGER finance_ledgers_delete BEFORE DELETE ON spyglass.finance_ledgers FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_parent_delete();
CREATE TRIGGER finance_accounts_identity BEFORE UPDATE ON spyglass.finance_accounts FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_identity();
CREATE TRIGGER finance_accounts_delete BEFORE DELETE ON spyglass.finance_accounts FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_parent_delete();
CREATE TRIGGER finance_entry_counters_delete BEFORE DELETE ON spyglass.finance_entry_number_counters FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_parent_delete();
CREATE TRIGGER finance_entries_guard BEFORE UPDATE OR DELETE ON spyglass.finance_entries FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_entry();
CREATE TRIGGER finance_entries_validate BEFORE INSERT OR UPDATE ON spyglass.finance_entries FOR EACH ROW EXECUTE FUNCTION spyglass.validate_finance_entry();
CREATE TRIGGER finance_entry_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON spyglass.finance_entry_lines FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_entry_child();
CREATE TRIGGER finance_entry_evidence_guard BEFORE INSERT OR UPDATE OR DELETE ON spyglass.finance_entry_evidence FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_entry_child();
CREATE TRIGGER finance_close_evidence_immutable BEFORE UPDATE OR DELETE ON spyglass.finance_ledger_close_evidence FOR EACH ROW EXECUTE FUNCTION spyglass.reject_finance_immutable_change();
CREATE TRIGGER finance_reconciliations_guard BEFORE UPDATE OR DELETE ON spyglass.finance_reconciliations FOR EACH ROW EXECUTE FUNCTION spyglass.protect_finance_reconciliation();
CREATE TRIGGER finance_reconciliation_evidence_immutable BEFORE UPDATE OR DELETE ON spyglass.finance_reconciliation_evidence FOR EACH ROW EXECUTE FUNCTION spyglass.reject_finance_immutable_change();
CREATE TRIGGER finance_events_immutable BEFORE UPDATE OR DELETE ON spyglass.finance_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_finance_immutable_change();

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['finance_ledgers','finance_ledger_close_evidence','finance_accounts','finance_entry_number_counters','finance_entries','finance_entry_lines','finance_entry_evidence','finance_reconciliations','finance_reconciliation_evidence','finance_events'] LOOP
        EXECUTE format('CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence()',table_name);
    END LOOP;
END $$;

CREATE FUNCTION spyglass.capture_finance_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
      counts:=COALESCE(NULLIF(current_setting('spyglass.finance_erasure_counts',true),'')::jsonb,'{}'::jsonb);
      current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
      PERFORM set_config('spyglass.finance_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION spyglass.add_finance_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.finance_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['finance_ledgers','finance_ledger_close_evidence','finance_accounts','finance_entry_number_counters','finance_entries','finance_entry_lines','finance_entry_evidence','finance_reconciliations','finance_reconciliation_evidence','finance_events'] LOOP
        EXECUTE format('CREATE TRIGGER %I BEFORE DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.capture_finance_erasure_count()',table_name||'_erasure_count',table_name);
    END LOOP;
END $$;
CREATE TRIGGER account_erasure_finance_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_finance_erasure_counts();

REVOKE ALL ON FUNCTION spyglass.protect_finance_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_finance_entry() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_finance_entry() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_finance_parent_delete() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_finance_entry_child() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.reject_finance_immutable_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_finance_reconciliation() FROM PUBLIC;

COMMIT;
