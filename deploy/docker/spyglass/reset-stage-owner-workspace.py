#!/usr/bin/env python3
"""Guarded, backed-up reset of the reviewed Stage owner's workspace (cell v90).

This is a test-fixture reset, not Account erasure. Identity, staff access,
commercial history, integrations and audit controls are retained. Object
versions remain in private storage so the protected snapshot can be restored.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid
from datetime import datetime, timezone

EMAIL = "tinfoyle@gmail.com"
ACCOUNT = "3341d937-77b1-4a22-b79e-63b3831baee6"
USER = "1a961ee1-712b-4745-8d09-e6895ef2146c"
GLOBAL = "spyglass-stage-global-db-1"
CELL = "spyglass-stage-cell-a-db-1"
ROOT = Path("/opt/spyglass-stage/evidence/workspace-resets")
PREFIXES = ("agent_", "attention_", "baseline_", "finance_", "knowledge_",
            "marketing_", "prototype_migration_", "runner_", "schedule_", "work_")
GLOBAL_BACKUP = ("entitlement_usage_counters", "entitlement_usage_reservations",
                 "ai_token_grants", "ai_token_reservations", "ai_token_ledger_entries")


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def ident(value):
    return '"' + value.replace('"', '""') + '"'


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


class DB:
    def __init__(self, container, evidence):
        self.errors = (evidence / (container + ".stderr")).open("w")
        self.process = subprocess.Popen(
            ["docker", "exec", "-i", container, "psql", "-X", "-qAt",
             "-v", "ON_ERROR_STOP=1", "-U", "spyglass_migrator", "-d", "spyglass"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.errors, text=True)
        self.exec("BEGIN; SET LOCAL lock_timeout='10s'; SET LOCAL statement_timeout='30s';")

    def exec(self, sql):
        marker = "reset_" + uuid.uuid4().hex
        self.process.stdin.write(sql + "\n\\echo " + marker + "\n")
        self.process.stdin.flush()
        lines = []
        while True:
            line = self.process.stdout.readline()
            if not line:
                raise RuntimeError("Database command failed; protected evidence contains details")
            if line.rstrip("\n") == marker:
                return lines
            if line.strip():
                lines.append(line.strip())

    def rows(self, query):
        return [json.loads(x) for x in self.exec("SELECT row_to_json(reset_row) FROM (" + query + ") reset_row;")]

    def close(self):
        if self.process.poll() is None:
            try:
                self.exec("ROLLBACK;")
                self.process.stdin.close()
                self.process.wait(timeout=5)
            except (RuntimeError, BrokenPipeError, subprocess.TimeoutExpired):
                self.process.kill()
        self.errors.close()


def snapshot(db, schema, tables, where):
    return {table: db.rows("SELECT * FROM " + ident(schema) + "." + ident(table) + " WHERE " + where)
            for table in tables}


def check_foreign_keys(db, tables):
    names = ",".join(literal("spyglass." + t) + "::regclass" for t in tables)
    checks = db.rows("""SELECT c.conname, c.conrelid::regclass::text AS child,
        c.confrelid::regclass::text AS parent,
        array_agg(a.attname ORDER BY k.ordinality) AS child_cols,
        array_agg(b.attname ORDER BY k.ordinality) AS parent_cols
        FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey,c.confkey)
        WITH ORDINALITY k(child_att,parent_att,ordinality)
        JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.child_att
        JOIN pg_attribute b ON b.attrelid=c.confrelid AND b.attnum=k.parent_att
        WHERE c.contype='f' AND (c.conrelid IN (""" + names + ") OR c.confrelid IN (" + names + ")) "
        "GROUP BY c.oid,c.conname,c.conrelid,c.confrelid")
    for check in checks:
        nonnull = " AND ".join("child." + ident(x) + " IS NOT NULL" for x in check["child_cols"])
        join = " AND ".join("child." + ident(x) + "=parent." + ident(y)
                            for x, y in zip(check["child_cols"], check["parent_cols"]))
        missing = db.rows("SELECT EXISTS(SELECT 1 FROM " + check["child"] + " child WHERE " + nonnull
                          + " AND NOT EXISTS(SELECT 1 FROM " + check["parent"] + " parent WHERE " + join + ")) AS bad")[0]["bad"]
        require(not missing, "Foreign-key consistency check failed: " + check["conname"])


def durable_write(path, content):
    with path.open("w") as output:
        output.write(content)
        output.flush()
        os.fsync(output.fileno())
    descriptor = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def other_account_fingerprints(db, schema, tables):
    result = {}
    for table in tables:
        result[table] = db.rows("SELECT count(*) AS n,md5(coalesce(string_agg(h,'' ORDER BY h),'')) AS hash FROM "
            "(SELECT md5(row_to_json(t)::text) AS h FROM " + ident(schema) + "." + ident(table)
            + " t WHERE account_id<>" + literal(ACCOUNT) + ") fingerprints")[0]
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("inspect", "rehearse", "execute"))
    parser.add_argument("email")
    parser.add_argument("account_id")
    parser.add_argument("confirm_email", nargs="?")
    args = parser.parse_args()
    require(args.email == EMAIL and args.account_id == ACCOUNT, "Only the reviewed Stage owner fixture is supported")
    require(args.mode != "execute" or args.confirm_email == EMAIL, "Execution requires the exact repeated email")
    for container in (GLOBAL, CELL, "spyglass-stage-cell-b-db-1"):
        label = subprocess.check_output(["docker", "inspect", "--format",
                  '{{ index .Config.Labels "com.docker.compose.project" }}', container], text=True).strip()
        require(label == "spyglass-stage", "Container is not in the exact Stage project")
    os.umask(0o077)
    ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    require(not ROOT.is_symlink() and ROOT.stat().st_mode & 0o077 == 0, "Evidence directory must be private and regular")
    evidence = Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-") + args.mode + "-", dir=ROOT))
    global_db = cell_db = None
    journal = {"mode": args.mode, "account_id": ACCOUNT, "user_id": USER,
               "email_sha256": hashlib.sha256(EMAIL.encode()).hexdigest(), "phase": "preparing"}
    def record(phase):
        journal["phase"] = phase
        durable_write(evidence / "journal.json", json.dumps(journal, indent=2) + "\n")
    try:
        global_db = DB(GLOBAL, evidence)
        cell_db = DB(CELL, evidence)
        for db, expected in ((global_db, 72), (cell_db, 90)):
            version = db.rows("SELECT count(*) AS n,max(version)::int AS v FROM public.spyglass_schema_migrations")[0]
            require(version == {"n": expected, "v": expected}, "Unreviewed schema version")
        # Lock the exact Account, its token entitlement lock and namespace. Every
        # normal cell writer takes a namespace share lock; this fences this
        # Account while keeping other Accounts available.
        global_db.exec("SELECT id FROM accounts WHERE id=" + literal(ACCOUNT) + " FOR UPDATE;")
        global_db.exec("SELECT spyglass_lock_account_entitlement_version(" + literal(ACCOUNT) + "::uuid);")
        owner = global_db.rows("SELECT u.id AS user_id,a.id AS account_id,a.state,a.cell_id,a.placement_generation "
            "FROM users u JOIN accounts a ON a.created_by_user_id=u.id JOIN memberships m "
            "ON m.account_id=a.id AND m.user_id=u.id WHERE u.primary_email=" + literal(EMAIL)
            + " AND a.id=" + literal(ACCOUNT) + " AND m.role='owner' AND m.state='active'")
        require(len(owner) == 1 and owner[0] == {"user_id": USER, "account_id": ACCOUNT, "state": "active",
                "cell_id": "cell-us-east-01", "placement_generation": 1}, "Owner or placement changed")
        require(global_db.rows("SELECT count(*) AS n FROM memberships WHERE account_id=" + literal(ACCOUNT)
                              + " AND state='active'")[0]["n"] == 1, "Additional active membership")
        ns = cell_db.rows("SELECT * FROM spyglass.account_namespaces WHERE account_id=" + literal(ACCOUNT) + " FOR UPDATE")
        require(len(ns) == 1 and ns[0]["state"] == "active" and ns[0]["placement_generation"] == 1, "Namespace changed")
        all_tables = [r["table_name"] for r in cell_db.rows("SELECT table_name FROM information_schema.columns "
                       "WHERE table_schema='spyglass' AND column_name='account_id' ORDER BY table_name")]
        targets = [t for t in all_tables if (t.startswith(PREFIXES) or t == "schedules") and not t.endswith("_operator_events")]
        preserved = [t for t in all_tables if t not in targets]
        # Fixed schema gate, explicit business families; global identity/billing,
        # staff audit, placement, receipts and Integration settings are excluded.
        account_where = "account_id=" + literal(ACCOUNT)
        backup = snapshot(cell_db, "spyglass", targets, account_where)
        kept = snapshot(cell_db, "spyglass", preserved, account_where)
        require(not kept.get("account_move_checkpoints"), "Account movement evidence exists")
        allowed_integrations = {"integration_connections", "integration_connection_revisions", "integration_events"}
        require(all(not rows for t, rows in kept.items() if t.startswith("integration_") and t not in allowed_integrations),
                "Integration work/credentials must be reviewed separately")
        require(all(r["state"] == "revoked" for r in kept.get("integration_connections", [])), "Active Integration could refill workspace")
        require(all(not d["legal_hold"] and d["retain_until"] is None for d in backup["knowledge_documents"]), "Document retention/hold exists")
        require(all(r["processing_state"] in ("completed", "execution_failed", "canceled") for r in backup["runner_invocation_queue"]),
                "A runner has outstanding work")
        require(all(r["status"] != "running" for r in backup["agent_invocations"]), "An agent is running")
        require(all(r["state"] in ("provisioned", "dead_letter") for r in backup["agent_dispatch_queue"]), "Agent dispatch is active")
        require(all(r["processing_state"] == "completed" for r in backup["work_capacity_release_queue"]), "Capacity release is outstanding")
        require(all(r["state"] == "linked" for r in backup["work_agent_execution_queue"]), "Work dispatch is outstanding")
        require(all(r["state"] == "paused" for r in backup["schedules"]), "Schedules must be paused")
        global_backup = snapshot(global_db, "public", GLOBAL_BACKUP, account_where)
        active_tokens = [r for r in global_backup["ai_token_reservations"] if r["state"] == "active"]
        # Release only admissions whose provider work never started. Never refund model
        # work that may have reached a provider.
        for reservation in active_tokens:
            invocation = next((r for r in backup["agent_invocations"] if r["id"] == reservation["request_id"]), None)
            require(invocation is not None and invocation["status"] == "queued", "Active AI reservation is not an unstarted invocation")
            require(not any(r["invocation_id"] == invocation["id"] for r in backup["runner_capability_events"]), "Active AI reservation has capability activity")
            exchanges = [r for r in backup["runner_invocation_exchanges"] if r["invocation_id"] == invocation["id"]]
            require(all(r["fetch_count"] == 0 and r["result_outcome"] is None for r in exchanges), "Active AI reservation request was fetched")
            require(any(r["invocation_id"] == invocation["id"] and r["state"] in ("dead_letter", "provisioned") for r in backup["agent_dispatch_queue"]), "Active AI reservation has unfinished dispatch")
        active_usage = [r for r in global_backup["entitlement_usage_reservations"] if r["state"] == "active"]
        for r in active_usage:
            require((r["package_code"], r["limit_code"]) == ("work", "active_items"), "Unreviewed active capacity")
            require(any(w["capacity_reservation_id"] == r["request_id"] for w in backup["work_items"]), "Capacity is not bound to reset Work")
        check_foreign_keys(cell_db, targets)
        other_cell = other_account_fingerprints(cell_db, "spyglass", targets)
        other_global = other_account_fingerprints(global_db, "public", GLOBAL_BACKUP)
        saved = {"cell": backup, "preserved_cell": kept, "global": global_backup, "owner": owner}
        raw = json.dumps(saved, sort_keys=True, separators=(",", ":")) + "\n"
        durable_write(evidence / "snapshot.json", raw)
        journal.update(snapshot_sha256=hashlib.sha256(raw.encode()).hexdigest(),
                       cell_counts={t: len(rows) for t, rows in backup.items() if rows},
                       released_ai_tokens=sum(r["maximum"] for r in active_tokens),
                       released_work_capacity=sum(r["amount"] for r in active_usage))
        record("inspected")
        if args.mode == "inspect":
            print(json.dumps(journal, indent=2)); print("Evidence: " + str(evidence)); return
        # Triggers enforce immutable production history. Only this superuser
        # Stage session suppresses them, inside the backed-up transaction. No
        # trigger definition, server setting or ordinary role is changed.
        cell_db.exec("SET LOCAL session_replication_role=replica;")
        for table in targets:
            cell_db.exec("DELETE FROM spyglass." + ident(table) + " WHERE " + account_where + ";")
        cell_db.exec("SET LOCAL session_replication_role=origin;")
        check_foreign_keys(cell_db, targets)
        require(snapshot(cell_db, "spyglass", preserved, account_where) == kept, "Preserved cell rows changed")
        require(all(not rows for rows in snapshot(cell_db, "spyglass", targets, account_where).values()), "Workspace not empty")
        # Retain usage history, release only the removed Work's active capacity.
        for reservation in active_usage:
            global_db.exec("UPDATE entitlement_usage_counters SET current_value=current_value-" + str(reservation["amount"])
                + ",version=version+1,updated_at=statement_timestamp() WHERE " + account_where
                + " AND package_code='work' AND limit_code='active_items'; UPDATE entitlement_usage_reservations "
                + "SET state='released',closed_at=statement_timestamp() WHERE id=" + literal(reservation["id"]) + ";")
        for reservation in active_tokens:
            allocations = global_db.rows("SELECT a.*,g.state AS grant_state,g.expires_at,g.reserved FROM ai_token_reservation_allocations a "
                  "JOIN ai_token_grants g ON g.id=a.grant_id WHERE a.reservation_id=" + literal(reservation["id"]))
            require(sum(a["amount"] for a in allocations) == reservation["maximum"], "AI allocation mismatch")
            for allocation in allocations:
                require(allocation["grant_state"] == "active" and allocation["expires_at"] is None
                        and allocation["reserved"] >= allocation["amount"], "Grant requires separate expiry reconciliation")
                global_db.exec("UPDATE ai_token_grants SET available=available+" + str(allocation["amount"])
                    + ",reserved=reserved-" + str(allocation["amount"]) + " WHERE id=" + literal(allocation["grant_id"])
                    + " AND " + account_where + ";")
            global_db.exec("UPDATE ai_token_reservations SET state='released',closed_at=statement_timestamp() WHERE id="
                + literal(reservation["id"]) + "; INSERT INTO ai_token_ledger_entries "
                "(id,account_id,reservation_id,event_key,kind,amount,created_at) VALUES ("
                + literal(str(uuid.uuid4())) + "," + literal(ACCOUNT) + "," + literal(reservation["id"]) + ","
                + literal("release:" + reservation["request_id"]) + ",'released'," + str(reservation["maximum"]) + ",statement_timestamp());")
        after_global = snapshot(global_db, "public", GLOBAL_BACKUP, account_where)
        require(not any(r["state"] == "active" for r in after_global["ai_token_reservations"]), "AI reservation remains active")
        require(not any(r["state"] == "active" for r in after_global["entitlement_usage_reservations"]), "Capacity reservation remains active")
        require(all(r["current_value"] == 0 for r in after_global["entitlement_usage_counters"]), "Capacity is not zero")
        before_grants = {r["id"]: r for r in global_backup["ai_token_grants"]}
        for grant in after_global["ai_token_grants"]:
            old = before_grants[grant["id"]]
            require(grant["consumed"] == old["consumed"] and grant["quantity"] == old["quantity"]
                    and grant["available"] + grant["reserved"] == old["available"] + old["reserved"], "AI Token value changed")
        durable_write(evidence / "global-after.json", json.dumps(after_global, sort_keys=True) + "\n")
        require(other_account_fingerprints(cell_db, "spyglass", targets) == other_cell, "Other Account cell data changed")
        require(other_account_fingerprints(global_db, "public", GLOBAL_BACKUP) == other_global, "Other Account global data changed")
        record("rehearsed")
        if args.mode == "execute":
            cell_db.exec("COMMIT;")
            record("cell_committed_global_pending")
            global_db.exec("COMMIT;")
            record("completed")
        else:
            cell_db.exec("ROLLBACK;"); global_db.exec("ROLLBACK;")
            record("rolled_back")
        print(json.dumps(journal, indent=2)); print("Evidence: " + str(evidence))
    finally:
        if cell_db:
            cell_db.close()
        if global_db:
            global_db.close()


if __name__ == "__main__":
    main()
