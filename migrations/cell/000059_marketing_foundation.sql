BEGIN;

CREATE TABLE spyglass.marketing_campaigns (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 160 AND name=btrim(name)),
    objective text NOT NULL CHECK (octet_length(objective) BETWEEN 1 AND 4000 AND objective=btrim(objective)),
    audience text NOT NULL CHECK (octet_length(audience) BETWEEN 1 AND 4000 AND audience=btrim(audience)),
    state text NOT NULL CHECK (state IN ('draft','active','paused','completed','archived')),
    active_release_id uuid,
    version bigint NOT NULL CHECK (version>0),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 200 AND created_by_id=btrim(created_by_id)),
    origin text NOT NULL CHECK (origin IN ('human','agent')),
    run_id uuid,
    invocation_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,invocation_id,run_id) REFERENCES spyglass.agent_invocations(account_id,id,run_id) ON DELETE RESTRICT,
    CHECK ((origin='human' AND created_by_kind='user' AND run_id IS NULL AND invocation_id IS NULL) OR
           (origin='agent' AND created_by_kind='workload' AND run_id IS NOT NULL AND invocation_id IS NOT NULL)),
    CHECK (state<>'active' OR active_release_id IS NOT NULL)
);

CREATE TABLE spyglass.marketing_campaign_channels (
    account_id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    channel text NOT NULL CHECK (channel IN ('email','web')),
    PRIMARY KEY (account_id,campaign_id,channel),
    FOREIGN KEY (account_id,campaign_id) REFERENCES spyglass.marketing_campaigns(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.marketing_assets (
    account_id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,campaign_id,id),
    FOREIGN KEY (account_id,campaign_id) REFERENCES spyglass.marketing_campaigns(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.marketing_asset_revisions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    kind text NOT NULL CHECK (kind IN ('copy','image','document')),
    title text NOT NULL CHECK (octet_length(title) BETWEEN 1 AND 240 AND title=btrim(title)),
    media_type text NOT NULL CHECK (octet_length(media_type) BETWEEN 3 AND 100 AND media_type=lower(btrim(media_type)) AND media_type ~ '^[^/[:space:]]+/[^/[:space:]]+$'),
    content_reference text NOT NULL CHECK (octet_length(content_reference) BETWEEN 1 AND 500 AND content_reference=btrim(content_reference)),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32 AND content_sha256<>decode(repeat('00',32),'hex')),
    content_bytes bigint NOT NULL CHECK (content_bytes>0),
    alternative_text text NOT NULL CHECK (octet_length(alternative_text)<=1000 AND alternative_text=btrim(alternative_text)),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 200 AND created_by_id=btrim(created_by_id)),
    origin text NOT NULL CHECK (origin IN ('human','agent')),
    run_id uuid,
    invocation_id uuid,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,campaign_id,id),
    UNIQUE (account_id,asset_id,revision),
    FOREIGN KEY (account_id,campaign_id,asset_id) REFERENCES spyglass.marketing_assets(account_id,campaign_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,invocation_id,run_id) REFERENCES spyglass.agent_invocations(account_id,id,run_id) ON DELETE RESTRICT,
    CHECK (kind<>'image' OR alternative_text<>''),
    CHECK ((origin='human' AND created_by_kind='user' AND run_id IS NULL AND invocation_id IS NULL) OR
           (origin='agent' AND created_by_kind='workload' AND run_id IS NOT NULL AND invocation_id IS NOT NULL))
);

CREATE TABLE spyglass.marketing_release_plans (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    campaign_version bigint NOT NULL CHECK (campaign_version>0),
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 160 AND name=btrim(name)),
    state text NOT NULL CHECK (state IN ('draft','submitted','approved','cancelled')),
    approval_id uuid,
    version bigint NOT NULL CHECK (version>0),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 200 AND created_by_id=btrim(created_by_id)),
    origin text NOT NULL CHECK (origin IN ('human','agent')),
    run_id uuid,
    invocation_id uuid,
    submitted_by_user_id uuid,
    approved_by_user_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,campaign_id,id),
    FOREIGN KEY (account_id,campaign_id) REFERENCES spyglass.marketing_campaigns(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,invocation_id,run_id) REFERENCES spyglass.agent_invocations(account_id,id,run_id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id,approval_id) REFERENCES spyglass.attention_consequential_approvals(account_id,id) ON DELETE RESTRICT,
    CHECK ((origin='human' AND created_by_kind='user' AND run_id IS NULL AND invocation_id IS NULL) OR
           (origin='agent' AND created_by_kind='workload' AND run_id IS NOT NULL AND invocation_id IS NOT NULL)),
    CHECK ((state='draft' AND submitted_by_user_id IS NULL AND approved_by_user_id IS NULL AND approval_id IS NULL) OR
           (state='submitted' AND submitted_by_user_id IS NOT NULL AND approved_by_user_id IS NULL AND approval_id IS NULL) OR
           (state='approved' AND submitted_by_user_id IS NOT NULL AND approved_by_user_id IS NOT NULL AND approval_id IS NOT NULL) OR
           (state='cancelled' AND submitted_by_user_id IS NOT NULL AND approved_by_user_id IS NULL AND approval_id IS NULL))
);

CREATE TABLE spyglass.marketing_release_channels (
    account_id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    release_id uuid NOT NULL,
    channel text NOT NULL CHECK (channel IN ('email','web')),
    PRIMARY KEY (account_id,release_id,channel),
    FOREIGN KEY (account_id,campaign_id,release_id) REFERENCES spyglass.marketing_release_plans(account_id,campaign_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.marketing_release_assets (
    account_id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    release_id uuid NOT NULL,
    asset_revision_id uuid NOT NULL,
    PRIMARY KEY (account_id,release_id,asset_revision_id),
    FOREIGN KEY (account_id,campaign_id,release_id) REFERENCES spyglass.marketing_release_plans(account_id,campaign_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,campaign_id,asset_revision_id) REFERENCES spyglass.marketing_asset_revisions(account_id,campaign_id,id) ON DELETE RESTRICT
);

-- active_release_id is validated under row lock by protect_marketing_campaign.
-- A reverse foreign key would create a campaign/release dependency cycle and
-- make the generic Account movement manifest impossible to topologically copy.

CREATE TABLE spyglass.marketing_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    aggregate_kind text NOT NULL CHECK (aggregate_kind IN ('campaign','asset','release')),
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('created','revised','asset_revised','submitted','approved','activated','paused','completed','archived','cancelled')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200 AND actor_id=btrim(actor_id)),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=4096 AND
        NOT (redacted_payload ?| ARRAY['name','objective','audience','title','alternative_text','content_reference','body','payload'])),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE INDEX marketing_campaigns_list ON spyglass.marketing_campaigns(account_id,state,updated_at DESC,id);
CREATE INDEX marketing_assets_campaign ON spyglass.marketing_assets(account_id,campaign_id,id);
CREATE INDEX marketing_asset_revisions_asset ON spyglass.marketing_asset_revisions(account_id,asset_id,revision DESC);
CREATE INDEX marketing_release_plans_campaign ON spyglass.marketing_release_plans(account_id,campaign_id,created_at DESC,id);
CREATE INDEX marketing_events_aggregate ON spyglass.marketing_events(account_id,aggregate_kind,aggregate_id,occurred_at,id);

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['marketing_campaigns','marketing_campaign_channels','marketing_assets','marketing_asset_revisions','marketing_release_plans','marketing_release_channels','marketing_release_assets','marketing_events'] LOOP
        EXECUTE format('ALTER TABLE spyglass.%I ENABLE ROW LEVEL SECURITY',table_name);
        EXECUTE format('ALTER TABLE spyglass.%I FORCE ROW LEVEL SECURITY',table_name);
        EXECUTE format('CREATE POLICY %I ON spyglass.%I USING (account_id=nullif(current_setting(''app.account_id'',true),'''')::uuid) WITH CHECK (account_id=nullif(current_setting(''app.account_id'',true),'''')::uuid)',table_name||'_isolation',table_name);
    END LOOP;
END $$;

CREATE FUNCTION spyglass.validate_marketing_asset_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected_revision bigint;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.account_id::text||':'||NEW.asset_id::text,0));
    SELECT COALESCE(max(revision),0)+1 INTO expected_revision
    FROM spyglass.marketing_asset_revisions
    WHERE account_id=NEW.account_id AND asset_id=NEW.asset_id;
    IF NEW.revision<>expected_revision THEN RAISE EXCEPTION 'Marketing asset revision is not sequential'; END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_marketing_campaign() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE release_row record; campaign_channels text[]; release_channels text[];
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
        RAISE EXCEPTION 'Marketing campaign deletion is prohibited';
    END IF;
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR
       NEW.created_by_id IS DISTINCT FROM OLD.created_by_id OR NEW.origin IS DISTINCT FROM OLD.origin OR NEW.run_id IS DISTINCT FROM OLD.run_id OR
       NEW.invocation_id IS DISTINCT FROM OLD.invocation_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.version<>OLD.version+1 THEN
        RAISE EXCEPTION 'Marketing campaign identity or version transition is invalid';
    END IF;
    IF OLD.state='archived' OR (OLD.state='active' AND NEW.state NOT IN ('paused','completed')) OR
       (OLD.state='completed' AND NEW.state<>'archived') OR
       (OLD.state='draft' AND NEW.state NOT IN ('draft','active','archived')) OR
       (OLD.state='paused' AND NEW.state NOT IN ('paused','active','completed','archived')) THEN
        RAISE EXCEPTION 'Marketing campaign state transition is invalid';
    END IF;
    IF OLD.state NOT IN ('draft','paused') AND (NEW.name IS DISTINCT FROM OLD.name OR NEW.objective IS DISTINCT FROM OLD.objective OR NEW.audience IS DISTINCT FROM OLD.audience) THEN
        RAISE EXCEPTION 'Active Marketing campaign content is immutable';
    END IF;
    IF NEW.state='active' AND (OLD.state<>'active' OR NEW.active_release_id IS DISTINCT FROM OLD.active_release_id) THEN
        SELECT release.state,release.campaign_version,approval.state AS approval_state,approval.expires_at
        INTO release_row
        FROM spyglass.marketing_release_plans release
        JOIN spyglass.attention_consequential_approvals approval
          ON approval.account_id=release.account_id AND approval.id=release.approval_id
        WHERE release.account_id=NEW.account_id AND release.campaign_id=NEW.id AND release.id=NEW.active_release_id;
        IF NOT FOUND OR release_row.state<>'approved' OR release_row.campaign_version<>OLD.version OR
           release_row.approval_state<>'approved' OR release_row.expires_at<=NEW.updated_at THEN
            RAISE EXCEPTION 'Marketing campaign release binding is invalid';
        END IF;
        SELECT array_agg(channel ORDER BY channel) INTO campaign_channels FROM spyglass.marketing_campaign_channels WHERE account_id=NEW.account_id AND campaign_id=NEW.id;
        SELECT array_agg(channel ORDER BY channel) INTO release_channels FROM spyglass.marketing_release_channels WHERE account_id=NEW.account_id AND release_id=NEW.active_release_id;
        IF campaign_channels IS NULL OR campaign_channels IS DISTINCT FROM release_channels THEN RAISE EXCEPTION 'Marketing campaign release channels do not match'; END IF;
    ELSIF NEW.active_release_id IS DISTINCT FROM OLD.active_release_id THEN
        RAISE EXCEPTION 'Marketing campaign active release transition is invalid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_marketing_campaign_channel() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_account uuid; row_campaign uuid; campaign_state text;
BEGIN
    IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
    row_account:=CASE WHEN TG_OP='DELETE' THEN OLD.account_id ELSE NEW.account_id END;
    row_campaign:=CASE WHEN TG_OP='DELETE' THEN OLD.campaign_id ELSE NEW.campaign_id END;
    SELECT state INTO campaign_state FROM spyglass.marketing_campaigns WHERE account_id=row_account AND id=row_campaign;
    IF campaign_state NOT IN ('draft','paused') THEN RAISE EXCEPTION 'Active Marketing campaign channels are immutable'; END IF;
    RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE FUNCTION spyglass.protect_marketing_release() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE approval_row record;
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
        RAISE EXCEPTION 'Marketing release deletion is prohibited';
    END IF;
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.campaign_id IS DISTINCT FROM OLD.campaign_id OR
       NEW.campaign_version IS DISTINCT FROM OLD.campaign_version OR NEW.name IS DISTINCT FROM OLD.name OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR
       NEW.created_by_id IS DISTINCT FROM OLD.created_by_id OR NEW.origin IS DISTINCT FROM OLD.origin OR NEW.run_id IS DISTINCT FROM OLD.run_id OR
       NEW.invocation_id IS DISTINCT FROM OLD.invocation_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.version<>OLD.version+1 THEN
        RAISE EXCEPTION 'Marketing release snapshot is immutable';
    END IF;
    IF (OLD.state='draft' AND NEW.state<>'submitted') OR (OLD.state='submitted' AND NEW.state NOT IN ('approved','cancelled')) OR
       (OLD.state='approved' AND NEW.state<>'cancelled') OR OLD.state='cancelled' THEN
        RAISE EXCEPTION 'Marketing release state transition is invalid';
    END IF;
    IF NEW.state='approved' THEN
        SELECT state,capability,decided_by_user_id,expires_at INTO approval_row
        FROM spyglass.attention_consequential_approvals WHERE account_id=NEW.account_id AND id=NEW.approval_id;
        IF NOT FOUND OR approval_row.state<>'approved' OR approval_row.capability<>'marketing.release.activate' OR
           approval_row.decided_by_user_id<>NEW.approved_by_user_id OR approval_row.expires_at<=NEW.updated_at THEN
            RAISE EXCEPTION 'Marketing release Attention approval is invalid';
        END IF;
    END IF;
    IF NEW.state='cancelled' AND EXISTS (
        SELECT 1 FROM spyglass.marketing_campaigns campaign
        WHERE campaign.account_id=NEW.account_id AND campaign.active_release_id=NEW.id AND campaign.state='active'
    ) THEN
        RAISE EXCEPTION 'Active Marketing campaign must be paused before release cancellation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.reject_marketing_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND (pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE'))) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Marketing revisions, release contents, and events are immutable';
END;
$$;

CREATE TRIGGER marketing_campaigns_guard BEFORE UPDATE OR DELETE ON spyglass.marketing_campaigns FOR EACH ROW EXECUTE FUNCTION spyglass.protect_marketing_campaign();
CREATE TRIGGER marketing_campaign_channels_guard BEFORE INSERT OR UPDATE OR DELETE ON spyglass.marketing_campaign_channels FOR EACH ROW EXECUTE FUNCTION spyglass.protect_marketing_campaign_channel();
CREATE TRIGGER marketing_assets_immutable BEFORE UPDATE OR DELETE ON spyglass.marketing_assets FOR EACH ROW EXECUTE FUNCTION spyglass.reject_marketing_immutable_change();
CREATE TRIGGER marketing_asset_revisions_sequence BEFORE INSERT ON spyglass.marketing_asset_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.validate_marketing_asset_revision();
CREATE TRIGGER marketing_asset_revisions_immutable BEFORE UPDATE OR DELETE ON spyglass.marketing_asset_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.reject_marketing_immutable_change();
CREATE TRIGGER marketing_release_plans_guard BEFORE UPDATE OR DELETE ON spyglass.marketing_release_plans FOR EACH ROW EXECUTE FUNCTION spyglass.protect_marketing_release();
CREATE TRIGGER marketing_release_channels_immutable BEFORE UPDATE OR DELETE ON spyglass.marketing_release_channels FOR EACH ROW EXECUTE FUNCTION spyglass.reject_marketing_immutable_change();
CREATE TRIGGER marketing_release_assets_immutable BEFORE UPDATE OR DELETE ON spyglass.marketing_release_assets FOR EACH ROW EXECUTE FUNCTION spyglass.reject_marketing_immutable_change();
CREATE TRIGGER marketing_events_immutable BEFORE UPDATE OR DELETE ON spyglass.marketing_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_marketing_immutable_change();

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['marketing_campaigns','marketing_campaign_channels','marketing_assets','marketing_asset_revisions','marketing_release_plans','marketing_release_channels','marketing_release_assets','marketing_events'] LOOP
        EXECUTE format('CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence()',table_name);
    END LOOP;
END $$;

CREATE FUNCTION spyglass.capture_marketing_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
      counts:=COALESCE(NULLIF(current_setting('spyglass.marketing_erasure_counts',true),'')::jsonb,'{}'::jsonb);
      current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
      PERFORM set_config('spyglass.marketing_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION spyglass.add_marketing_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.marketing_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['marketing_campaigns','marketing_campaign_channels','marketing_assets','marketing_asset_revisions','marketing_release_plans','marketing_release_channels','marketing_release_assets','marketing_events'] LOOP
        EXECUTE format('CREATE TRIGGER %I BEFORE DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.capture_marketing_erasure_count()',table_name||'_erasure_count',table_name);
    END LOOP;
END $$;
CREATE TRIGGER account_erasure_marketing_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_marketing_erasure_counts();

REVOKE ALL ON FUNCTION spyglass.validate_marketing_asset_revision() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_marketing_campaign() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_marketing_campaign_channel() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_marketing_release() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.reject_marketing_immutable_change() FROM PUBLIC;

COMMIT;
