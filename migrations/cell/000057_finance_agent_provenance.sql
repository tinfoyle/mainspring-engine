BEGIN;

CREATE UNIQUE INDEX agent_invocations_finance_provenance
ON spyglass.agent_invocations(account_id,id,run_id);

ALTER TABLE spyglass.finance_entries
ADD CONSTRAINT finance_entries_agent_provenance
FOREIGN KEY (account_id,invocation_id,run_id)
REFERENCES spyglass.agent_invocations(account_id,id,run_id)
ON DELETE RESTRICT;

COMMIT;
