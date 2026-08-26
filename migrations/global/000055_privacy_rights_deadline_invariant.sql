BEGIN;

UPDATE public.privacy_rights_requests
SET response_due_at = ((requested_at AT TIME ZONE 'UTC' + INTERVAL '1 month') AT TIME ZONE 'UTC')
WHERE response_due_at IS DISTINCT FROM ((requested_at AT TIME ZONE 'UTC' + INTERVAL '1 month') AT TIME ZONE 'UTC');

ALTER TABLE public.privacy_rights_requests
    ADD CONSTRAINT privacy_rights_response_calendar_month CHECK (
        response_due_at = ((requested_at AT TIME ZONE 'UTC' + INTERVAL '1 month') AT TIME ZONE 'UTC')
    );

COMMIT;
