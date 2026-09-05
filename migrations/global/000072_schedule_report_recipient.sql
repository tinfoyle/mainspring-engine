BEGIN;
CREATE FUNCTION public.spyglass_schedule_report_recipient(p_account uuid,p_user uuid,p_cell text)
RETURNS TABLE(email text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT u.primary_email FROM public.users u
 JOIN public.memberships m ON m.user_id=u.id AND m.account_id=p_account AND m.state='active'
 JOIN public.accounts a ON a.id=m.account_id AND a.state='active' AND a.cell_id=p_cell
 JOIN public.entitlement_snapshots e ON e.account_id=a.id AND e.version=a.entitlement_version
 WHERE u.id=p_user AND u.state='active' AND u.email_verified_at IS NOT NULL
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(e.effective_packages) p WHERE p->>'code'='agents' AND p->>'mode'='enabled')
$$;
REVOKE ALL ON FUNCTION public.spyglass_schedule_report_recipient(uuid,uuid,text) FROM PUBLIC;
COMMIT;
