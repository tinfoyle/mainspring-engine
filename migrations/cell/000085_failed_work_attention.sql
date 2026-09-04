BEGIN;

-- A bounded technical failure is still an owner-facing decision, not a silent
-- queue state. Ask for a narrower first outcome or additional direction in
-- Your Turn; answering the request uses the existing Work continuation path.
CREATE OR REPLACE FUNCTION spyglass.open_failed_work_agent_attention(
    p_account_id uuid,p_work_item_id uuid,p_work_event_id uuid,p_correlation_id text,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE work_row record; question_text text; fact_key_text text; request_id uuid; attention_event_id uuid; inserted_count integer;
DECLARE seed text;
BEGIN
    IF p_account_id IS NULL OR p_work_item_id IS NULL OR p_work_event_id IS NULL OR
       p_correlation_id IS NULL OR char_length(p_correlation_id) NOT BETWEEN 1 AND 200 OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid failed Work Agent attention request';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT work.id,work.title,work.state,work.responsibility,work.assignee_persona_id INTO work_row
    FROM spyglass.work_items work WHERE work.account_id=p_account_id AND work.id=p_work_item_id;
    IF NOT FOUND OR work_row.state<>'waiting' OR work_row.responsibility<>'persona' OR work_row.assignee_persona_id IS NULL THEN
        RETURN false;
    END IF;
    question_text:='The operations agent could not finish “'||work_row.title||'” after three attempts. What outcome should it produce first, or what extra direction would help?';
    fact_key_text:='agent.owner_question.'||substr(encode(sha256(convert_to(question_text,'UTF8')),'hex'),1,32);
    seed:=p_work_event_id::text||'/failed-work-attention';
    request_id:=(substr(md5(seed||'/request'),1,8)||'-'||substr(md5(seed||'/request'),9,4)||'-'||
        substr(md5(seed||'/request'),13,4)||'-'||substr(md5(seed||'/request'),17,4)||'-'||
        substr(md5(seed||'/request'),21,12))::uuid;
    attention_event_id:=(substr(md5(seed||'/event'),1,8)||'-'||substr(md5(seed||'/event'),9,4)||'-'||
        substr(md5(seed||'/event'),13,4)||'-'||substr(md5(seed||'/event'),17,4)||'-'||
        substr(md5(seed||'/event'),21,12))::uuid;
    INSERT INTO spyglass.attention_information_requests
        (account_id,id,parent_work_item_id,fact_key,scope_kind,question,requested_by_kind,requested_by_id,state,reason,version,created_at,updated_at)
    VALUES (p_account_id,request_id,p_work_item_id,fact_key_text,'account',question_text,'workload',
            'agent:'||work_row.assignee_persona_id::text,'open','',1,p_now,p_now)
    ON CONFLICT (account_id,id) DO NOTHING;
    GET DIAGNOSTICS inserted_count=ROW_COUNT;
    IF inserted_count=0 THEN RETURN false; END IF;
    INSERT INTO spyglass.attention_events
        (account_id,id,aggregate_kind,information_request_id,event_type,from_version,to_version,actor_kind,actor_id,reason,
         correlation_id,redacted_payload,occurred_at)
    VALUES (p_account_id,attention_event_id,'information_request',request_id,'information_requested',0,1,'workload',
            'agent:'||work_row.assignee_persona_id::text,'',p_correlation_id,
            jsonb_build_object('state','open','scope_kind','account','reason','agent_execution_failed'),p_now);
    RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION spyglass.open_failed_work_agent_attention(uuid,uuid,uuid,text,timestamptz) FROM PUBLIC;

CREATE OR REPLACE FUNCTION spyglass.enqueue_failed_work_agent_attention() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    PERFORM spyglass.open_failed_work_agent_attention(
        NEW.account_id,NEW.work_item_id,NEW.id,NEW.correlation_id,NEW.occurred_at
    );
    RETURN NULL;
END;
$$;
REVOKE ALL ON FUNCTION spyglass.enqueue_failed_work_agent_attention() FROM PUBLIC;

CREATE TRIGGER work_item_failed_agent_attention
AFTER INSERT ON spyglass.work_item_events
FOR EACH ROW
WHEN (NEW.event_type='transitioned' AND NEW.reason='Agent execution requires operator attention')
EXECUTE FUNCTION spyglass.enqueue_failed_work_agent_attention();

-- Backfill Work that reached the bounded fallback under migration 84 before
-- this trigger existed.
DO $$
DECLARE failed record;
BEGIN
    FOR failed IN
        SELECT event.account_id,event.work_item_id,event.id,event.correlation_id,event.occurred_at
        FROM spyglass.work_item_events event
        JOIN spyglass.work_items work ON work.account_id=event.account_id AND work.id=event.work_item_id
        WHERE event.event_type='transitioned' AND event.reason='Agent execution requires operator attention'
          AND work.state='waiting' AND work.responsibility='persona'
          AND NOT EXISTS (
              SELECT 1 FROM spyglass.attention_information_requests request
              WHERE request.account_id=work.account_id AND request.parent_work_item_id=work.id AND request.state='open'
          )
    LOOP
        PERFORM spyglass.open_failed_work_agent_attention(
            failed.account_id,failed.work_item_id,failed.id,failed.correlation_id,failed.occurred_at
        );
    END LOOP;
END;
$$;

COMMIT;
