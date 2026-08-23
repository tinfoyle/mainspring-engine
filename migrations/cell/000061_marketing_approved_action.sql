BEGIN;

INSERT INTO spyglass.runner_action_executors
    (capability,executor_id,executor_version,policy_version,enabled,max_definite_attempts,retry_base_delay,retryable_error_codes,updated_at)
VALUES ('marketing.release.activate','marketing.release',1,1,true,1,interval '30 seconds',ARRAY[]::text[],statement_timestamp());

COMMIT;
