BEGIN;
-- Email the same useful result sections that customers see in Boardroom.
-- Never include raw proposal payloads, internal IDs, or the setup schema.
CREATE FUNCTION spyglass.agent_report_text(p_body text,p_result jsonb)
RETURNS text LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
 SELECT concat_ws(E'\n\n',nullif(p_body,''),(
  SELECT string_agg(section.title || E'\n' || section.content,E'\n\n' ORDER BY section.position)
  FROM (
   SELECT v.title,v.position,(
    SELECT string_agg('- ' || line.value,E'\n' ORDER BY line.ordinality)
    FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(p_result->v.key)='array' THEN p_result->v.key ELSE '[]'::jsonb END)
      WITH ORDINALITY AS line(value,ordinality)
   ) AS content
   FROM (VALUES ('findings','Findings',1),('recommendations','Recommendations',2),('questions','Questions',3)) AS v(key,title,position)
  ) AS section WHERE section.content IS NOT NULL
 ),(
  SELECT 'Sources' || E'\n' || string_agg('- ' || (citation.value->>'label'),E'\n' ORDER BY citation.ordinality)
  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p_result->'citations')='array' THEN p_result->'citations' ELSE '[]'::jsonb END)
   WITH ORDINALITY AS citation(value,ordinality)
  WHERE jsonb_typeof(citation.value->'label')='string' AND citation.value->>'label'<>''
 ));
$$;
REVOKE ALL ON FUNCTION spyglass.agent_report_text(text,jsonb) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.spyglass_begin_schedule_report(p_account uuid,p_id uuid,p_lease uuid,p_now timestamptz)
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
 SELECT string_agg(spyglass.agent_report_text(m.body,m.structured_result),E'\n\n' ORDER BY m.sequence) INTO report_body FROM spyglass.agent_messages m
 WHERE m.account_id=p_account AND m.run_id=d.run_id AND m.role='persona';
 IF report_body IS NULL OR length(report_body)>200000 THEN
  UPDATE spyglass.schedule_report_deliveries SET state='failed',error_code='report_content_unavailable',updated_at=p_now,lease_id=NULL,lease_expires_at=NULL WHERE account_id=p_account AND id=p_id; RETURN;
 END IF;
 UPDATE spyglass.schedule_report_deliveries SET state='sending',updated_at=p_now WHERE account_id=p_account AND id=p_id;
 RETURN QUERY SELECT report_subject,report_body,conversation,d.schedule_id,(s.execution_template->>'boardroom_id')::uuid;
END;
$$;


COMMIT;
