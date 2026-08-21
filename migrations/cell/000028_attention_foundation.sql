BEGIN;

CREATE TABLE spyglass.attention_information_requests (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    parent_work_item_id uuid NOT NULL,
    fact_key text NOT NULL CHECK (fact_key ~ '^[a-z][a-z0-9._:/-]{0,127}$'),
    scope_kind text NOT NULL CHECK (scope_kind IN ('account','work_item','conversation')),
    scope_work_item_id uuid,
    scope_conversation_id uuid,
    question text NOT NULL CHECK (char_length(question) BETWEEN 3 AND 4000),
    requested_by_kind text NOT NULL CHECK (requested_by_kind IN ('user','workload')),
    requested_by_id text NOT NULL CHECK (char_length(requested_by_id) BETWEEN 1 AND 200 AND requested_by_id=btrim(requested_by_id)),
    state text NOT NULL CHECK (state IN ('open','answered','canceled')),
    fact_id uuid,
    fact_version bigint,
    answered_by_kind text CHECK (answered_by_kind IS NULL OR answered_by_kind IN ('user','workload')),
    answered_by_id text,
    answered_at timestamptz,
    canceled_by_kind text CHECK (canceled_by_kind IS NULL OR canceled_by_kind IN ('user','workload')),
    canceled_by_id text,
    reason text NOT NULL DEFAULT '' CHECK (char_length(reason)<=1000),
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,parent_work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,scope_work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,scope_conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    CHECK ((scope_kind='account' AND scope_work_item_id IS NULL AND scope_conversation_id IS NULL) OR
           (scope_kind='work_item' AND scope_work_item_id IS NOT NULL AND scope_conversation_id IS NULL) OR
           (scope_kind='conversation' AND scope_work_item_id IS NULL AND scope_conversation_id IS NOT NULL)),
    CHECK ((answered_by_id IS NULL AND answered_by_kind IS NULL) OR
           (answered_by_id IS NOT NULL AND char_length(answered_by_id) BETWEEN 1 AND 200 AND answered_by_id=btrim(answered_by_id) AND answered_by_kind IS NOT NULL)),
    CHECK ((canceled_by_id IS NULL AND canceled_by_kind IS NULL) OR
           (canceled_by_id IS NOT NULL AND char_length(canceled_by_id) BETWEEN 1 AND 200 AND canceled_by_id=btrim(canceled_by_id) AND canceled_by_kind IS NOT NULL)),
    CHECK ((state='open' AND fact_id IS NULL AND fact_version IS NULL AND answered_by_id IS NULL AND answered_at IS NULL AND canceled_by_id IS NULL AND reason='') OR
           (state='answered' AND fact_id IS NOT NULL AND fact_version>0 AND answered_by_id IS NOT NULL AND answered_at>=created_at AND canceled_by_id IS NULL AND reason='') OR
           (state='canceled' AND fact_id IS NULL AND fact_version IS NULL AND answered_by_id IS NULL AND answered_at IS NULL AND canceled_by_id IS NOT NULL AND char_length(btrim(reason)) BETWEEN 3 AND 1000))
);

CREATE TABLE spyglass.attention_work_reviews (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    work_version bigint NOT NULL CHECK (work_version>0),
    proposal_sha256 bytea NOT NULL CHECK (octet_length(proposal_sha256)=32),
    question text NOT NULL CHECK (char_length(question) BETWEEN 3 AND 4000),
    requested_by_kind text NOT NULL CHECK (requested_by_kind IN ('user','workload')),
    requested_by_id text NOT NULL CHECK (char_length(requested_by_id) BETWEEN 1 AND 200 AND requested_by_id=btrim(requested_by_id)),
    reviewer_user_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('open','approved','changes_requested','canceled','invalidated')),
    decision text CHECK (decision IS NULL OR decision IN ('approve','request_changes')),
    decision_reason text NOT NULL DEFAULT '' CHECK (char_length(decision_reason)<=1000),
    decided_by_user_id uuid,
    decided_at timestamptz,
    canceled_by_kind text CHECK (canceled_by_kind IS NULL OR canceled_by_kind IN ('user','workload')),
    canceled_by_id text,
    cancel_reason text NOT NULL DEFAULT '' CHECK (char_length(cancel_reason)<=1000),
    invalidated_at timestamptz,
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    CHECK ((decision IS NULL AND decision_reason='' AND decided_by_user_id IS NULL AND decided_at IS NULL) OR
           (decision IS NOT NULL AND char_length(btrim(decision_reason)) BETWEEN 3 AND 1000 AND decided_by_user_id=reviewer_user_id AND decided_at>=created_at)),
    CHECK ((canceled_by_id IS NULL AND canceled_by_kind IS NULL) OR
           (canceled_by_id IS NOT NULL AND char_length(canceled_by_id) BETWEEN 1 AND 200 AND canceled_by_id=btrim(canceled_by_id) AND canceled_by_kind IS NOT NULL)),
    CHECK ((state='open' AND decision IS NULL AND canceled_by_id IS NULL AND cancel_reason='' AND invalidated_at IS NULL) OR
           (state='approved' AND decision='approve' AND canceled_by_id IS NULL AND cancel_reason='' AND invalidated_at IS NULL) OR
           (state='changes_requested' AND decision='request_changes' AND canceled_by_id IS NULL AND cancel_reason='' AND invalidated_at IS NULL) OR
           (state='canceled' AND decision IS NULL AND canceled_by_id IS NOT NULL AND char_length(btrim(cancel_reason)) BETWEEN 3 AND 1000 AND invalidated_at IS NULL) OR
           (state='invalidated' AND canceled_by_id IS NULL AND cancel_reason='' AND invalidated_at>=created_at))
);

CREATE TABLE spyglass.attention_consequential_approvals (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    operation_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    work_item_id uuid,
    capability text NOT NULL CHECK (capability ~ '^[a-z][a-z0-9.:/-]{0,127}$'),
    canonical_payload bytea NOT NULL CHECK (octet_length(canonical_payload) BETWEEN 2 AND 262144),
    input_sha256 bytea NOT NULL CHECK (octet_length(input_sha256)=32),
    hash_version smallint NOT NULL CHECK (hash_version=1),
    evidence_sha256 bytea NOT NULL CHECK (octet_length(evidence_sha256)=32),
    proposer_kind text NOT NULL CHECK (proposer_kind IN ('user','workload')),
    proposer_id text NOT NULL CHECK (char_length(proposer_id) BETWEEN 1 AND 200 AND proposer_id=btrim(proposer_id)),
    policy_version bigint NOT NULL CHECK (policy_version>0),
    require_independent_review boolean NOT NULL,
    expires_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('open','approved','rejected','canceled','invalidated','expired')),
    decision text CHECK (decision IS NULL OR decision IN ('approve','reject')),
    decision_reason text NOT NULL DEFAULT '' CHECK (char_length(decision_reason)<=1000),
    decided_by_user_id uuid,
    decided_at timestamptz,
    canceled_by_kind text CHECK (canceled_by_kind IS NULL OR canceled_by_kind IN ('user','workload')),
    canceled_by_id text,
    cancel_reason text NOT NULL DEFAULT '' CHECK (char_length(cancel_reason)<=1000),
    canceled_at timestamptz,
    invalidated_at timestamptz,
    expired_at timestamptz,
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,operation_id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,invocation_id) REFERENCES spyglass.agent_invocations(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    CHECK (expires_at>created_at AND expires_at<=created_at+interval '24 hours'),
    CHECK ((decision IS NULL AND decision_reason='' AND decided_by_user_id IS NULL AND decided_at IS NULL) OR
           (decision IS NOT NULL AND char_length(btrim(decision_reason)) BETWEEN 3 AND 1000 AND decided_by_user_id IS NOT NULL AND decided_at>=created_at AND decided_at<expires_at)),
    CHECK ((canceled_by_id IS NULL AND canceled_by_kind IS NULL) OR
           (canceled_by_id IS NOT NULL AND char_length(canceled_by_id) BETWEEN 1 AND 200 AND canceled_by_id=btrim(canceled_by_id) AND canceled_by_kind IS NOT NULL)),
    CHECK ((state='open' AND decision IS NULL AND canceled_by_id IS NULL AND cancel_reason='' AND canceled_at IS NULL AND invalidated_at IS NULL AND expired_at IS NULL) OR
           (state='approved' AND decision='approve' AND canceled_by_id IS NULL AND cancel_reason='' AND canceled_at IS NULL AND invalidated_at IS NULL AND expired_at IS NULL) OR
           (state='rejected' AND decision='reject' AND canceled_by_id IS NULL AND cancel_reason='' AND canceled_at IS NULL AND invalidated_at IS NULL AND expired_at IS NULL) OR
           (state='canceled' AND decision IS NULL AND canceled_by_id IS NOT NULL AND char_length(btrim(cancel_reason)) BETWEEN 3 AND 1000 AND canceled_at>=created_at AND canceled_at<expires_at AND invalidated_at IS NULL AND expired_at IS NULL) OR
           (state='invalidated' AND canceled_by_id IS NULL AND cancel_reason='' AND canceled_at IS NULL AND invalidated_at>=created_at AND invalidated_at<expires_at AND expired_at IS NULL) OR
           (state='expired' AND canceled_by_id IS NULL AND cancel_reason='' AND canceled_at IS NULL AND invalidated_at IS NULL AND expired_at>=expires_at))
);

CREATE TABLE spyglass.attention_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    aggregate_kind text NOT NULL CHECK (aggregate_kind IN ('information_request','work_review','consequential_approval')),
    information_request_id uuid,
    work_review_id uuid,
    consequential_approval_id uuid,
    event_type text NOT NULL CHECK (event_type IN ('information_requested','information_answered','information_canceled','review_requested','review_decided','review_canceled','review_invalidated','approval_requested','approval_decided','approval_canceled','approval_invalidated','approval_expired')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200 AND actor_id=btrim(actor_id)),
    reason text NOT NULL CHECK (char_length(reason)<=1000),
    correlation_id text NOT NULL CHECK (char_length(correlation_id) BETWEEN 1 AND 200),
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=8192),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,information_request_id) REFERENCES spyglass.attention_information_requests(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,work_review_id) REFERENCES spyglass.attention_work_reviews(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,consequential_approval_id) REFERENCES spyglass.attention_consequential_approvals(account_id,id) ON DELETE CASCADE,
    CHECK ((aggregate_kind='information_request' AND information_request_id IS NOT NULL AND work_review_id IS NULL AND consequential_approval_id IS NULL AND event_type IN ('information_requested','information_answered','information_canceled')) OR
           (aggregate_kind='work_review' AND information_request_id IS NULL AND work_review_id IS NOT NULL AND consequential_approval_id IS NULL AND event_type IN ('review_requested','review_decided','review_canceled','review_invalidated')) OR
           (aggregate_kind='consequential_approval' AND information_request_id IS NULL AND work_review_id IS NULL AND consequential_approval_id IS NOT NULL AND event_type IN ('approval_requested','approval_decided','approval_canceled','approval_invalidated','approval_expired')))
);

CREATE INDEX attention_information_open ON spyglass.attention_information_requests(account_id,updated_at,id) WHERE state='open';
CREATE INDEX attention_information_parent ON spyglass.attention_information_requests(account_id,parent_work_item_id,state,id);
CREATE INDEX attention_information_requirement ON spyglass.attention_information_requests(account_id,fact_key,scope_kind,scope_work_item_id,scope_conversation_id,id) WHERE state='open';
CREATE INDEX attention_reviews_open ON spyglass.attention_work_reviews(account_id,reviewer_user_id,updated_at,id) WHERE state='open';
CREATE INDEX attention_reviews_work ON spyglass.attention_work_reviews(account_id,work_item_id,created_at,id);
CREATE INDEX attention_approvals_open ON spyglass.attention_consequential_approvals(account_id,updated_at,id) WHERE state='open';
CREATE INDEX attention_approvals_expiry ON spyglass.attention_consequential_approvals(account_id,expires_at,id) WHERE state IN ('open','approved');
CREATE INDEX attention_events_aggregate ON spyglass.attention_events(account_id,aggregate_kind,information_request_id,work_review_id,consequential_approval_id,occurred_at,id);

ALTER TABLE spyglass.attention_information_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_information_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_work_reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_work_reviews FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_consequential_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_consequential_approvals FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.attention_events FORCE ROW LEVEL SECURITY;
CREATE POLICY attention_information_requests_isolation ON spyglass.attention_information_requests USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY attention_work_reviews_isolation ON spyglass.attention_work_reviews USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY attention_consequential_approvals_isolation ON spyglass.attention_consequential_approvals USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY attention_events_isolation ON spyglass.attention_events USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.reject_attention_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        -- Foreign-key cascades are the only serving-path deletion mechanism.
        -- They are required for exact Account erasure and parent retirement.
        IF pg_trigger_depth()>1 THEN RETURN OLD; END IF;
        IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
    END IF;
    RAISE EXCEPTION 'Attention events are immutable';
END;
$$;

CREATE FUNCTION spyglass.capture_attention_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        counts:=COALESCE(NULLIF(current_setting('spyglass.attention_erasure_counts',true),'')::jsonb,'{}'::jsonb);
        current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
        PERFORM set_config('spyglass.attention_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION spyglass.add_attention_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.attention_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER attention_events_immutable BEFORE UPDATE OR DELETE ON spyglass.attention_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_attention_event_change();
CREATE TRIGGER attention_information_erasure_count BEFORE DELETE ON spyglass.attention_information_requests FOR EACH ROW EXECUTE FUNCTION spyglass.capture_attention_erasure_count();
CREATE TRIGGER attention_reviews_erasure_count BEFORE DELETE ON spyglass.attention_work_reviews FOR EACH ROW EXECUTE FUNCTION spyglass.capture_attention_erasure_count();
CREATE TRIGGER attention_approvals_erasure_count BEFORE DELETE ON spyglass.attention_consequential_approvals FOR EACH ROW EXECUTE FUNCTION spyglass.capture_attention_erasure_count();
CREATE TRIGGER attention_events_erasure_count BEFORE DELETE ON spyglass.attention_events FOR EACH ROW EXECUTE FUNCTION spyglass.capture_attention_erasure_count();
CREATE TRIGGER account_erasure_attention_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_attention_erasure_counts();

CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.attention_information_requests FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.attention_work_reviews FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.attention_consequential_approvals FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.attention_events FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.reject_attention_event_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.capture_attention_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_attention_erasure_counts() FROM PUBLIC;

COMMIT;
