BEGIN;

-- A Docker runner can be launched during a rolling Stage restart before its
-- private broker DNS name is available. If it exits before fetching the
-- encrypted request, there is no result payload to project. Requeue only this
-- exact, side-effect-free failure shape for Persona-owned Work. The runner's
-- deterministic identity still prevents a second execution from being
-- confused with any other invocation.
WITH recoverable AS (
    SELECT queue.account_id,queue.invocation_id
    FROM spyglass.runner_invocation_queue queue
    JOIN spyglass.runner_invocation_exchanges exchange
      ON exchange.account_id=queue.account_id AND exchange.invocation_id=queue.invocation_id
    JOIN spyglass.agent_invocations invocation
      ON invocation.account_id=queue.account_id AND invocation.id=queue.invocation_id
    JOIN spyglass.work_items work
      ON work.account_id=invocation.account_id AND work.run_id=invocation.run_id
    JOIN spyglass.agent_result_projection_queue projection
      ON projection.account_id=invocation.account_id AND projection.invocation_id=invocation.id
    WHERE queue.processing_state='execution_failed'
      AND exchange.fetch_count=0
      AND exchange.result_digest IS NULL
      AND invocation.status='queued'
      AND work.state='in_progress'
      AND work.responsibility='persona'
      AND projection.state='dead_letter'
      AND projection.last_error_code='payload_unavailable'
)
UPDATE spyglass.runner_invocation_queue queue SET
    processing_state='failed',
    next_attempt_at=statement_timestamp(),
    lease_id=NULL,
    lease_expires_at=NULL,
    job_name=NULL,
    last_error_code='runner_prefetch_failed',
    launched_at=NULL,
    completed_at=NULL
FROM recoverable
WHERE queue.account_id=recoverable.account_id
  AND queue.invocation_id=recoverable.invocation_id;

UPDATE spyglass.agent_result_projection_queue projection SET
    state='pending',
    attempt_count=0,
    next_attempt_at=statement_timestamp(),
    lease_id=NULL,
    lease_expires_at=NULL,
    last_error_code=NULL,
    projected_at=NULL,
    updated_at=statement_timestamp()
FROM spyglass.runner_invocation_queue queue
JOIN spyglass.runner_invocation_exchanges exchange
  ON exchange.account_id=queue.account_id AND exchange.invocation_id=queue.invocation_id
JOIN spyglass.agent_invocations invocation
  ON invocation.account_id=queue.account_id AND invocation.id=queue.invocation_id
JOIN spyglass.work_items work
  ON work.account_id=invocation.account_id AND work.run_id=invocation.run_id
WHERE projection.account_id=queue.account_id
  AND projection.invocation_id=queue.invocation_id
  AND queue.processing_state='failed'
  AND queue.last_error_code='runner_prefetch_failed'
  AND exchange.fetch_count=0
  AND exchange.result_digest IS NULL
  AND invocation.status='queued'
  AND work.state='in_progress'
  AND work.responsibility='persona'
  AND projection.state='dead_letter'
  AND projection.last_error_code='payload_unavailable';

COMMIT;
