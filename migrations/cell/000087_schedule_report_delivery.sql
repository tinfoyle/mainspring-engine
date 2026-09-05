BEGIN;
CREATE TABLE spyglass.schedule_report_deliveries (
 account_id uuid NOT NULL, id uuid NOT NULL, schedule_id uuid NOT NULL, run_id uuid NOT NULL,
 recipient_user_id uuid NOT NULL, definition jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','claimed','sending','sent','failed','unknown','cancelled')),
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 3),
 lease_id uuid, lease_expires_at timestamptz, next_attempt_at timestamptz NOT NULL,
 error_code text NOT NULL DEFAULT '', created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 PRIMARY KEY(account_id,id), UNIQUE(account_id,run_id),
 FOREIGN KEY(account_id,id) REFERENCES spyglass.schedule_occurrences(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,run_id) REFERENCES spyglass.agent_runs(account_id,id) ON DELETE CASCADE
);
CREATE INDEX schedule_report_ready ON spyglass.schedule_report_deliveries(next_attempt_at,created_at,id) WHERE state IN ('queued','claimed','sending');
ALTER TABLE spyglass.schedule_report_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.schedule_report_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY schedule_report_isolation ON spyglass.schedule_report_deliveries
 USING(account_id=nullif(current_setting('app.account_id',true),'')::uuid)
 WITH CHECK(account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedule_report_deliveries
 FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER schedule_report_erasure_count BEFORE DELETE ON spyglass.schedule_report_deliveries
 FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();

CREATE FUNCTION spyglass.enqueue_schedule_report() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE template jsonb; creator uuid;
BEGIN
 IF NEW.outcome<>'dispatched' THEN RETURN NEW; END IF;
 SELECT execution_template,created_by_user_id INTO template,creator FROM spyglass.schedules WHERE account_id=NEW.account_id AND id=NEW.schedule_id;
 IF template->>'email_self'='true' THEN
  INSERT INTO spyglass.schedule_report_deliveries(account_id,id,schedule_id,run_id,recipient_user_id,definition,next_attempt_at,created_at,updated_at)
  VALUES(NEW.account_id,NEW.id,NEW.schedule_id,NEW.run_id,creator,template,NEW.occurred_at,NEW.occurred_at,NEW.occurred_at);
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER schedule_report_enqueue AFTER INSERT ON spyglass.schedule_occurrences FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_schedule_report();

CREATE FUNCTION spyglass.cancel_changed_schedule_reports() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
 IF NEW.state<>'active' OR NEW.execution_template IS DISTINCT FROM OLD.execution_template THEN
  UPDATE spyglass.schedule_report_deliveries SET state='cancelled',error_code='schedule_changed',updated_at=statement_timestamp(),lease_id=NULL,lease_expires_at=NULL
   WHERE account_id=NEW.account_id AND schedule_id=NEW.id AND state IN ('queued','claimed');
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER cancel_changed_schedule_reports AFTER UPDATE ON spyglass.schedules FOR EACH ROW EXECUTE FUNCTION spyglass.cancel_changed_schedule_reports();

CREATE FUNCTION public.spyglass_claim_schedule_report(p_lease uuid,p_now timestamptz)
RETURNS TABLE(account_id uuid,id uuid,recipient_user_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE picked spyglass.schedule_report_deliveries;
BEGIN
 IF p_lease IS NULL OR p_now IS NULL THEN RAISE EXCEPTION 'invalid report claim'; END IF;
 -- A crash after send begins is ambiguous. Never automatically send it again.
 UPDATE spyglass.schedule_report_deliveries d SET state='unknown',error_code='delivery_outcome_unknown',updated_at=p_now,lease_id=NULL,lease_expires_at=NULL
 WHERE d.state='sending' AND d.lease_expires_at<p_now;
 UPDATE spyglass.schedule_report_deliveries d SET state='queued',lease_id=NULL,lease_expires_at=NULL,updated_at=p_now
 WHERE d.state='claimed' AND d.lease_expires_at<p_now AND d.attempt_count<3;
 UPDATE spyglass.schedule_report_deliveries d SET state='failed',error_code='delivery_attempts_exhausted',updated_at=p_now,lease_id=NULL,lease_expires_at=NULL
 WHERE d.state='claimed' AND d.lease_expires_at<p_now AND d.attempt_count>=3;
 UPDATE spyglass.schedule_report_deliveries d SET state='failed',error_code='agent_run_failed',updated_at=p_now
 FROM spyglass.agent_runs r WHERE r.account_id=d.account_id AND r.id=d.run_id AND d.state='queued' AND r.state IN ('failed','canceled','partially_failed');
 UPDATE spyglass.schedule_report_deliveries d SET state='failed',error_code='agent_run_timeout',updated_at=p_now
 WHERE d.state='queued' AND d.created_at<p_now-interval '24 hours';
 SELECT d.* INTO picked FROM spyglass.schedule_report_deliveries d
 JOIN spyglass.agent_runs r ON r.account_id=d.account_id AND r.id=d.run_id AND r.state='succeeded'
 JOIN spyglass.schedules s ON s.account_id=d.account_id AND s.id=d.schedule_id AND s.state='active' AND s.execution_template=d.definition
 WHERE d.state='queued' AND d.next_attempt_at<=p_now AND d.attempt_count<3
 ORDER BY d.next_attempt_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN; END IF;
 UPDATE spyglass.schedule_report_deliveries d SET state='claimed',lease_id=p_lease,lease_expires_at=p_now+interval '2 minutes',attempt_count=d.attempt_count+1,updated_at=p_now
 WHERE d.account_id=picked.account_id AND d.id=picked.id;
 RETURN QUERY SELECT picked.account_id,picked.id,picked.recipient_user_id,picked.attempt_count+1;
END;
$$;

CREATE FUNCTION public.spyglass_begin_schedule_report(p_account uuid,p_id uuid,p_lease uuid,p_now timestamptz)
RETURNS TABLE(subject text,body text,conversation_id uuid,schedule_id uuid,boardroom_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE d spyglass.schedule_report_deliveries; s spyglass.schedules; report_body text; report_subject text; conversation uuid;
BEGIN
 IF p_account IS NULL OR p_id IS NULL OR p_lease IS NULL OR p_now IS NULL THEN RETURN; END IF;
 SELECT * INTO d FROM spyglass.schedule_report_deliveries WHERE account_id=p_account AND id=p_id FOR UPDATE;
 IF NOT FOUND OR d.state<>'claimed' OR d.lease_id<>p_lease OR d.lease_expires_at<p_now THEN RETURN; END IF;
 SELECT * INTO s FROM spyglass.schedules WHERE account_id=p_account AND id=d.schedule_id FOR SHARE;
 IF NOT FOUND OR s.state<>'active' OR s.execution_template<>d.definition OR s.created_by_user_id<>d.recipient_user_id THEN
  UPDATE spyglass.schedule_report_deliveries SET state='cancelled',error_code='schedule_changed',updated_at=p_now,lease_id=NULL,lease_expires_at=NULL WHERE account_id=p_account AND id=p_id; RETURN;
 END IF;
 SELECT c.subject,r.conversation_id INTO report_subject,conversation FROM spyglass.agent_runs r JOIN spyglass.agent_conversations c ON c.account_id=r.account_id AND c.id=r.conversation_id WHERE r.account_id=p_account AND r.id=d.run_id AND r.state='succeeded';
 IF NOT FOUND THEN RETURN; END IF;
 SELECT string_agg(m.body,E'\n\n' ORDER BY m.sequence) INTO report_body FROM spyglass.agent_messages m
 WHERE m.account_id=p_account AND m.run_id=d.run_id AND m.role='persona';
 IF report_body IS NULL OR length(report_body)>200000 THEN
  UPDATE spyglass.schedule_report_deliveries SET state='failed',error_code='report_content_unavailable',updated_at=p_now,lease_id=NULL,lease_expires_at=NULL WHERE account_id=p_account AND id=p_id; RETURN;
 END IF;
 UPDATE spyglass.schedule_report_deliveries SET state='sending',updated_at=p_now WHERE account_id=p_account AND id=p_id;
 RETURN QUERY SELECT report_subject,report_body,conversation,d.schedule_id,(s.execution_template->>'boardroom_id')::uuid;
END;
$$;

CREATE FUNCTION public.spyglass_finish_schedule_report(p_account uuid,p_id uuid,p_lease uuid,p_state text,p_code text,p_now timestamptz)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE changed integer;
BEGIN
 IF p_account IS NULL OR p_id IS NULL OR p_lease IS NULL OR p_now IS NULL OR p_state IS NULL OR p_code IS NULL THEN RETURN false; END IF;
 IF p_state NOT IN ('sent','failed','unknown','cancelled','queued') OR p_code!~'^[a-z_]{0,80}$' THEN RAISE EXCEPTION 'invalid report outcome'; END IF;
 UPDATE spyglass.schedule_report_deliveries d SET
 state=CASE WHEN p_state='queued' AND d.attempt_count>=3 THEN 'failed' ELSE p_state END,
 error_code=p_code,updated_at=p_now,next_attempt_at=p_now+interval '1 minute',lease_id=NULL,lease_expires_at=NULL
 WHERE d.account_id=p_account AND d.id=p_id AND d.lease_id=p_lease AND d.lease_expires_at>=p_now
 AND ((d.state='claimed' AND p_state IN ('cancelled','failed','queued')) OR (d.state='sending' AND p_state IN ('sent','failed','unknown','queued')));
 GET DIAGNOSTICS changed=ROW_COUNT; RETURN changed=1;
END;
$$;
REVOKE ALL ON FUNCTION spyglass.enqueue_schedule_report(),spyglass.cancel_changed_schedule_reports(),public.spyglass_claim_schedule_report(uuid,timestamptz),
 public.spyglass_begin_schedule_report(uuid,uuid,uuid,timestamptz),public.spyglass_finish_schedule_report(uuid,uuid,uuid,text,text,timestamptz) FROM PUBLIC;
COMMIT;
