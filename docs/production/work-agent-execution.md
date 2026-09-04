# Work-to-Agent execution

Status: durable P3.1 construction implemented; automatic dispatch, successful owner-question projection, capacity deferral and exhausted-failure recovery are connected on Stage RC.41. Broader product-journey certification remains.

Persona assignment is a Work-owned execution boundary. It does not call the Agent service synchronously and it does not treat the lower-level Agent dispatch queue as proof that Work was linked.

## Durable flow

1. A User assigns an active, published Persona to eligible nonterminal Work.
2. The Work transaction invalidates any unstarted prior intent and inserts one deterministic execution intent containing the exact Work version and text, initiating User, Persona version, Boardroom policy version, Run ID and Conversation ID.
3. The shared cell Agent worker claims only execution and Account identifiers with a bounded lease.
4. Inside Account RLS it restores the immutable Persona snapshot. Over its cell-specific mTLS identity it asks the global admission API to reauthorize the initiating User's current Membership, Account placement, enabled Agents mutation mode, entitlement version and `concurrent_runs` limit. No session cookie, bearer token or saved route proof crosses this boundary.
5. The worker heartbeats the lease, prepares the same validated Run-plan digest and bounded runner operation identities used by ordinary Agent starts, and invokes one execute-only PostgreSQL function.
6. That transaction rechecks lease, Work version/assignment/text, Persona and Boardroom state/policy, serializes Account Run capacity, creates Conversation, User message, Run, plan turn, invocation and execution plan, lets the established Agent-dispatch trigger enqueue the invocation, links Work, moves it to `in_progress`, appends a redacted event, and marks the intent linked.

## Crash and change convergence

- Before claim or before Run creation: no Run exists; an expired lease is reclaimable.
- During creation/link: PostgreSQL rolls the whole transaction back, leaving a retryable leased or reclaimed intent. A Run cannot commit without its Work link.
- Response lost after commit: deterministic replay returns the existing exact linked pair. Even if the caller cannot observe that reply, durable state is already one linked Run.
- Reassignment while leased: the Work transaction marks the old intent dead-letter and clears its lease; the stale worker's heartbeat/start fails closed.
- Temporary admission, database or capacity failure: bounded exponential retry releases the lease and schedules another attempt.
- Membership/package/placement drift, Work version drift, retired Persona or changed Boardroom policy: the intent becomes dead-letter without creating a Run.
- After dispatch: the established digest-bound Agent dispatch, runner control and result projection queues retain their own leases and reconciliation. They consume the committed immutable plan and cannot create a second Work link.

## Authority and operations

The Account-owned immutable intent is forced through RLS. Its separate cross-Account claim queue contains identifiers and technical lease state only; neither table has a direct worker grant, and both are reachable solely through no-PUBLIC-execute claim/exact-leased-load/heartbeat/failure/stats/start-link functions. The worker may otherwise read only immutable Agent context needed to construct a request. The admission API accepts this operation only behind its exact client-certificate allowlist and binds the verified SPIFFE URI back to the requested cell. Docker stage certificates and Kubernetes workload Secret placeholders use distinct cell Agent-dispatch identities.

Health status adds content-free `work_pending`, `work_ready`, `work_leased`, `work_retrying`, `work_linked`, `work_dead_letter`, oldest-ready-age, processed and failure fields. Account movement refuses to freeze a source with a pending, retrying or leased Work execution. Exact erasure counts both intent and technical queue through their cascade, and no customer text appears in global admission logs or metrics.

The disposable PostgreSQL integration suite covers atomic creation/link, existing-pair reconciliation, dispatch enqueue, stale-lease revocation, direct-table privilege denial, forced Account scope and Run uniqueness. Processor tests cover permanent/transient classification, capacity backoff, lease loss, invalid snapshots and deterministic reconciliation.
