# Resetting the Stage owner's workspace

Status: performed successfully on 2026-09-07, after the owner explicitly selected **Clear all workspace test data**. Stage remains on RC63; this operator script requires no application image change.

## Which script to use

`reset-stage-signup.sh` still exists, but only accepts an empty, inactive signup shell. Its inspection correctly refused this activated owner Account. Do not relax that script's guards to reset a populated workspace.

The separate `deploy/docker/spyglass/reset-stage-owner-workspace.py` is pinned to the reviewed Stage owner fixture, exact Stage Docker labels, global schema 72 and cell schema 90. It refuses any other email/Account, changed owner/placement, additional active members, document retention/holds, active integrations or unreviewed integration records, and outstanding workers. It is not a generic customer reset or a privacy-erasure tool.

Target: `tinfoyle@gmail.com`, User `1a961ee1-712b-4745-8d09-e6895ef2146c`, Account `3341d937-77b1-4a22-b79e-63b3831baee6`, cell A, placement generation 1.

## Scope

Clears this Account's Knowledge, claims/evidence, Documents, Baseline, Work, attention items, agent teams/personas/conversations/runs, schedules, Finance test records, Marketing test records and related workspace processing history. The reset does not create replacement agents or a new Baseline.

Preserves Google login, identity, memberships, staff role, authenticator, billing, subscription/entitlement state, AI Token grants and historical usage, security/operator audit controls, Account placement, route receipts and Integration settings. The one existing Integration is revoked; the reset does not activate it.

Only active Work capacity tied to removed Work is released. An active AI Token reservation can be released only for a queued invocation with no capability events, no fetched runner request/result, and terminal or dead-lettered execution. Previously consumed tokens are not refunded or erased. Expiring/non-active grants require separate reconciliation and cause refusal.

## Operator commands

Run repository and SSH commands in UbuntuRojo, as described in the [release handoff](stage-release-operator-handoff.md). The reviewed script is installed on the VPS at `/opt/spyglass-stage/maintenance/reset-stage-owner-workspace.py` from commit `1d2611b`.

```bash
python3 /opt/spyglass-stage/maintenance/reset-stage-owner-workspace.py inspect \
  tinfoyle@gmail.com 3341d937-77b1-4a22-b79e-63b3831baee6
python3 /opt/spyglass-stage/maintenance/reset-stage-owner-workspace.py rehearse \
  tinfoyle@gmail.com 3341d937-77b1-4a22-b79e-63b3831baee6
```

Inspection makes no application data changes. Rehearsal performs the complete reset and reconciliation in transactions, validates foreign keys, verifies that preserved rows and other Accounts' fingerprints are unchanged, and rolls everything back. Both write private operator evidence.

After the owner authorizes the concrete scope and rehearsal passes:

```bash
python3 /opt/spyglass-stage/maintenance/reset-stage-owner-workspace.py execute \
  tinfoyle@gmail.com 3341d937-77b1-4a22-b79e-63b3831baee6 tinfoyle@gmail.com
```

Execution repeats the guards and backup. It locks the exact global Account/entitlement and cell namespace so ordinary writers for this Account cannot race the reset. It uses a transaction-local superuser trigger suppression solely for the Stage test data delete, restores normal trigger mode, and explicitly validates every foreign key touching the reset tables. It never modifies trigger definitions or serving-role permissions.

## Backup and recovery

Each run creates a mode-700 directory under `/opt/spyglass-stage/evidence/workspace-resets` with mode-600 files. `snapshot.json` contains the exact removed cell rows, preserved cell rows and affected global accounting rows. `journal.json` records scope, counts, snapshot SHA-256 and phase. `global-after.json` records the expected accounting outcome. Writes are flushed to disk before commit.

Document source/extracted object versions remain in private MinIO storage for restoration; they are no longer discoverable through live Document or Knowledge rows. This is a reversible test-data reset, not physical data erasure. Backups contain private test data and must stay on the VPS. Do not publish them or place them in Git.

Cell and global commits are separate. If interrupted with `cell_committed_global_pending`, do not blindly rerun or restore the whole database. Inspect the exact reservation IDs and release event keys to determine whether the global commit completed, then reconcile those exact rows against `snapshot.json` and `global-after.json` under the same Account locks. Never overwrite later billing activity. A successful cell commit with failed global commit requires reconciliation before treating the reset as complete.

Restoring workspace rows is a separate reviewed maintenance operation: fence the exact Account, use the snapshot and schema 90, restore only the captured rows in a transaction with the same explicit foreign-key verification, and omit generated columns when reconstructing inserts. Restore accounting only if no later activity conflicts. The snapshot's document object keys/version IDs identify the retained private objects. There is no automatic restore command in this script; do not treat a generic whole-database restore as Account-scoped recovery.

## Completed reset evidence

Execution directory: `/opt/spyglass-stage/evidence/workspace-resets/20260907T184137Z-execute-i5szic06`.

- Journal phase: `completed`.
- Snapshot SHA-256: `3a5803ff78cb1b216a878926eaf9b3b1c900dbf69b028fccd8ae881e26e92aef`.
- Cleared 15 facts, 19 claims, one document, one Baseline assessment, five Work items, three agent teams/four personas, 25 conversations, 41 runs, four paused schedules, two Finance ledgers and two Marketing campaigns, with their dependent records.
- Released two Work capacity units and the old unfetched September 3 invocation's 2,500 reserved AI Tokens.
- Final AI Token balance: 8,982 available, zero reserved, 1,018 consumed. Historical billing and usage retained.
- Database checks confirmed empty workspace tables and no active capacity/token reservations.
- Wrong-target and missing-confirmation checks refused execution. Full rehearsal passed before execution; other Accounts' fingerprints and preserved cell rows matched.
- Browser Google login succeeded as the same owner. Workspace showed **Create an agent team to start a conversation**; Knowledge showed zero pending/current items and Documents was empty.
- Staff state remained active and the authenticator row remained present. No authenticator challenge was exercised during this reset.
- Stage remained at 53 running containers, 52 healthy and zero unhealthy.

Historical verification links to the cleared fixture records, including the Kimi test conversation, will no longer resolve. Their committed verification reports and this protected snapshot remain the test evidence.

To begin again, refresh the workspace, use **Set up agents** to create a team, add your real business information or documents, and start a new conversation. A stale browser tab may still display an old selected conversation until it reloads.
