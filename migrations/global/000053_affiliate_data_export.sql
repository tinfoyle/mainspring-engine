BEGIN;

-- Customer portability receives a purpose-built JSON projection. Keeping this
-- projection inside a SECURITY DEFINER function prevents callers from gaining
-- raw access to referred Accounts, provider identifiers, staff actors, or
-- free-form operational reasons.
CREATE FUNCTION spyglass_export_affiliate_data(p_user_id uuid) RETURNS jsonb
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
SELECT jsonb_build_object(
    'schema_version', 1,
    'generated_at', statement_timestamp(),
    'enrollment', (
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'affiliate_id', e.affiliate_id,
            'user_id', e.user_id,
            'settlement_account_id', e.settlement_account_id,
            'public_code', e.public_code,
            'terms_version', e.terms_version,
            'rule_version', e.rule_version,
            'state', e.state,
            'version', e.version,
            'created_at', e.created_at,
            'updated_at', e.updated_at
        ))
        FROM public.affiliate_enrollments e
        WHERE e.user_id = p_user_id
    ),
    'public_codes', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'public_code', h.public_code,
            'enrollment_version', h.enrollment_version,
            'activated_at', h.activated_at,
            'replaced_at', h.replaced_at
        )) ORDER BY h.enrollment_version)
        FROM public.affiliate_public_code_history h
        JOIN public.affiliate_enrollments e ON e.affiliate_id = h.affiliate_id
        WHERE e.user_id = p_user_id
    ), '[]'::jsonb),
    'enrollment_events', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'event_id', v.event_id,
            'version', v.version,
            'action', v.action,
            'state', v.state,
            'occurred_at', v.occurred_at
        ) ORDER BY v.occurred_at, v.event_id)
        FROM public.affiliate_enrollment_events v
        JOIN public.affiliate_enrollments e ON e.affiliate_id = v.affiliate_id
        WHERE e.user_id = p_user_id
    ), '[]'::jsonb),
    'attribution_summary', (
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'total', count(*),
            'reserved', count(*) FILTER (WHERE a.state = 'reserved'),
            'locked', count(*) FILTER (WHERE a.state = 'locked'),
            'canceled', count(*) FILTER (WHERE a.state = 'canceled'),
            'earliest_at', min(a.created_at),
            'latest_at', max(a.created_at)
        ))
        FROM public.affiliate_attributions a
        JOIN public.affiliate_enrollments e ON e.affiliate_id = a.affiliate_id
        WHERE e.user_id = p_user_id
    ),
    'commission_entries', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'entry_id', c.entry_id,
            'rule_version', c.rule_version,
            'cycle', c.cycle,
            'kind', c.kind,
            'state', c.state,
            'amount_minor', c.amount_minor,
            'currency', c.currency,
            'reverses_entry_id', c.reverses_entry_id,
            'available_at', c.available_at,
            'created_at', c.created_at
        )) ORDER BY c.created_at, c.entry_id)
        FROM public.affiliate_commission_entries c
        JOIN public.affiliate_enrollments e ON e.affiliate_id = c.affiliate_id
        WHERE e.user_id = p_user_id
    ), '[]'::jsonb),
    'support_requests', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'request_id', r.request_id,
            'kind', r.kind,
            'commission_entry_id', r.commission_entry_id,
            'state', r.state,
            'outcome', r.outcome,
            'version', r.version,
            'created_at', r.created_at,
            'updated_at', r.updated_at
        )) ORDER BY r.created_at, r.request_id)
        FROM public.affiliate_support_requests r
        WHERE r.user_id = p_user_id
    ), '[]'::jsonb),
    'support_events', COALESCE((
        SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'event_id', v.event_id,
            'request_id', v.request_id,
            'version', v.version,
            'action', v.action,
            'state', v.state,
            'outcome', v.outcome,
            'occurred_at', v.occurred_at
        )) ORDER BY v.occurred_at, v.event_id)
        FROM public.affiliate_support_request_events v
        JOIN public.affiliate_support_requests r ON r.request_id = v.request_id
        WHERE r.user_id = p_user_id
    ), '[]'::jsonb)
)
$$;

REVOKE ALL ON FUNCTION spyglass_export_affiliate_data(uuid) FROM PUBLIC;

COMMIT;
