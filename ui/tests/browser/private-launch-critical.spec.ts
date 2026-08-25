import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page, type Route } from "@playwright/test";

const accountID = "10000000-0000-4000-8000-000000000001";
const userID = "20000000-0000-4000-8000-000000000002";
const account = {
  account_id: accountID,
  account_type: "paid",
  account_version: 1,
  cell_id: "cell-us-east-01",
  display_name: "Northstar Studio",
  placement_generation: 1,
  role: "owner",
  slug: "northstar-studio",
  owner_enrollment_required: false,
  entitlements: {
    account_id: accountID,
    catalog_version: 2,
    evaluated_at: "2026-08-24T20:00:00Z",
    version: 3,
    packages: [
      { code: "work", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "knowledge", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "agents", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "finance", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "integrations", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "marketing", version: 1, mode: "enabled", sources: ["subscription"] }
    ]
  }
};
const secondAccountID = "10000000-0000-4000-8000-000000000010";
const secondAccount = {
  ...account,
  account_id: secondAccountID,
  display_name: "Harbor Workshop",
  role: "member",
  slug: "harbor-workshop",
  entitlements: { ...account.entitlements, account_id: secondAccountID }
};
const consent = {
  analytics: true,
  decided: true,
  marketing: false,
  policy_version: 1,
  renewal_required: false,
  surface: "private"
};
const privacyRightsRequest = {
  request_id: "11000000-0000-4000-8000-000000000011",
  kind: "erasure",
  scope: "affiliate",
  state: "submitted",
  requested_at: "2026-08-25T12:00:00Z",
  response_due_at: "2026-09-24T12:00:00Z",
  updated_at: "2026-08-25T12:00:00Z",
  verified_at: "2026-08-25T12:00:00Z"
};
const catalog = {
  version: 2,
  published_at: "2026-08-24T20:00:00Z",
  limits: [],
  packages: [],
  plans: [{
    code: "team",
    version: 1,
    name: "Team",
    description: "A governed operating workspace.",
    packages: { work: "enabled", knowledge: "enabled" }
  }],
  offers: [{
    code: "team-monthly-v1",
    plan_code: "team",
    plan_version: 1,
    currency: "USD",
    amount_minor: 4900,
    billing_interval: "month",
    effective_from: "2026-08-20T20:00:00Z"
  }]
};
const ownerMembership = {
  membership_id: "80000000-0000-4000-8000-000000000008",
  user_id: userID,
  display_name: "Casey Owner",
  email: "casey@example.com",
  role: "owner",
  state: "active",
  version: 5,
  created_at: "2026-08-20T20:00:00Z"
};
const memberMembership = {
  membership_id: "90000000-0000-4000-8000-000000000009",
  user_id: "a0000000-0000-4000-8000-00000000000a",
  display_name: "Morgan Member",
  email: "morgan@example.com",
  role: "member",
  state: "active",
  version: 2,
  created_at: "2026-08-21T20:00:00Z"
};
const queuedExport = {
  id: "b0000000-0000-4000-8000-00000000000b",
  account_id: accountID,
  requested_by: userID,
  cell_id: "cell-us-east-01",
  placement_generation: 1,
  account_version: 1,
  state: "queued",
  version: 3,
  attempt_count: 0,
  requested_at: "2026-08-25T12:00:00Z",
  expires_at: "2026-09-01T12:00:00Z"
};
const workItem = {
  id: "c0000000-0000-4000-8000-00000000000c",
  number: 17,
  depth: 0,
  kind: "ticket",
  title: "Confirm the launch checklist",
  description: "Verify the governed release boundary.",
  state: "in_progress",
  priority: "high",
  assignment: { responsibility: "shared" },
  provenance: { source: "manual", created_by: { kind: "user", id: userID } },
  version: 4,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:05:00Z"
};
const knowledgeClaim = {
  id: "d0000000-0000-4000-8000-00000000000d",
  account_id: accountID,
  scope: { kind: "account" },
  key: "launch.release_window",
  value: "August 31",
  value_sha256: "a".repeat(64),
  hash_version: 1,
  confidence: 920,
  sensitivity: "internal",
  citations: [{
    evidence_id: "e0000000-0000-4000-8000-00000000000e",
    evidence_kind: "document_revision",
    relation: "supports",
    locator: "Launch plan, page 4"
  }],
  state: "proposed",
  proposed_by: { kind: "workload", id: "planning-agent" },
  version: 3,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:05:00Z"
};
const knowledgeFact = {
  id: "f0000000-0000-4000-8000-00000000000f",
  current_claim_id: knowledgeClaim.id,
  scope: { kind: "account" },
  key: "organization.legal_name",
  sensitivity: "internal",
  state: "active",
  revision: 2,
  accepted_at: "2026-08-23T20:00:00Z",
  updated_at: "2026-08-23T20:00:00Z"
};
const baselineID = "11000000-0000-4000-8000-000000000011";
const baseline = {
  id: baselineID,
  account_id: accountID,
  catalog_version: "baseline-evidence-2026-08-22",
  scope_policy_version: "baseline-scope-v1",
  state: "interview",
  answers: [],
  requirements: [],
  created_by_user_id: userID,
  version: 1,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:00:00Z"
};
const agentRoom = {
  id: "12000000-0000-4000-8000-000000000012",
  manager_persona_id: "13000000-0000-4000-8000-000000000013",
  name: "Operating review",
  purpose: "Resolve launch constraints.",
  state: "active",
  version: 4,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:00:00Z"
};
const agentPersona = {
  id: agentRoom.manager_persona_id,
  boardroom_id: agentRoom.id,
  state: "active",
  latest_version: 2,
  persona_version_id: "14000000-0000-4000-8000-000000000014",
  name: "Operations Lead",
  role: "Synthesis manager",
  description: "Synthesizes evidence and open risks.",
  system_instructions: "Review the evidence and state bounded recommendations.",
  content_digest: "b".repeat(64),
  policy: {
    provider: "openai",
    model: "gpt-5.4",
    fallback_models: [],
    maximum_input_tokens: 10000,
    maximum_output_tokens: 2000,
    maximum_cost_micros: 100000,
    maximum_tool_steps: 2,
    citation_policy: "required",
    action_policy: "propose",
    tools: [],
    output_schema: {}
  },
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:00:00Z"
};
const agentConversation = {
  id: "15000000-0000-4000-8000-000000000015",
  boardroom_id: agentRoom.id,
  subject: "Launch readiness",
  state: "open",
  message_count: 2,
  created_by: userID,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:05:00Z"
};
const agentRunID = "16000000-0000-4000-8000-000000000016";
const agentMessage = {
  id: "17000000-0000-4000-8000-000000000017",
  conversation_id: agentConversation.id,
  sequence: 2,
  role: "persona",
  body: "Two readiness gaps remain.",
  run_id: agentRunID,
  invocation_id: "18000000-0000-4000-8000-000000000018",
  persona_version_id: agentPersona.persona_version_id,
  created_at: "2026-08-24T20:05:00Z",
  result: {
    contribution: "Two readiness gaps remain.",
    findings: ["Security review is open."],
    recommendations: ["Close the review."],
    questions: [],
    citations: [],
    delegations: [],
    confidence: "high",
    proposed_actions: [{ kind: "marketing.release.publish", reason: "Publish only after approval.", payload: {}, evidence: ["release-checklist"] }]
  }
};
const agentRun = {
  id: agentRunID,
  boardroom_id: agentRoom.id,
  conversation_id: agentConversation.id,
  subject: agentConversation.subject,
  prompt: "Review launch readiness.",
  mode: "selected",
  state: "succeeded",
  context: [],
  context_digest: "c".repeat(64),
  entitlement_version: 3,
  plan_digest: "d".repeat(64),
  policy_version: 2,
  invocation_ids: [],
  invocations: [],
  turns: [],
  resolutions: [],
  user_message_id: "19000000-0000-4000-8000-000000000019",
  created_at: "2026-08-24T20:00:00Z"
};
const schedule = {
  id: "21000000-0000-4000-8000-000000000021",
  account_id: accountID,
  name: "Monday launch review",
  timezone: "America/New_York",
  recurrence: { frequency: "weekly", weekdays: [1], local_hour: 9, local_minute: 30, gap_policy: "next_valid", overlap_policy: "first" },
  missed_run_policy: "catch_up_one",
  template: { boardroom_id: agentRoom.id, mode: "selected", persona_ids: [agentPersona.id], subject: "Launch review", prompt: "Review launch readiness.", work_item_ids: null, knowledge_fact_ids: null, knowledge_document_ids: null, baseline_assessment_ids: null },
  state: "active",
  next_run_at: "2026-08-31T13:30:00Z",
  version: 4,
  created_by: userID,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:00:00Z"
};
const financeLedger = { id: "22000000-0000-4000-8000-000000000022", account_id: accountID, name: "Operating", code: "OPS", description: "Primary book", currency: "USD", state: "active", version: 3, created_by: { kind: "user", id: userID }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" };
const financeSummary = { ...financeLedger, account_count: 2, draft_count: 1, income_minor: 5000, expense_minor: 2000, net_minor: 3000 };
const financeEntry = { id: "23000000-0000-4000-8000-000000000023", account_id: accountID, ledger_id: financeLedger.id, number: 8, entry_date: "2026-08-24T00:00:00Z", description: "Monthly close", reference: "CLOSE-8", currency: "USD", total_minor: 5000, state: "draft", version: 5, lines: [{ account_id: "cash", memo: "", debit_minor: 5000, credit_minor: 0 }, { account_id: "income", memo: "", debit_minor: 0, credit_minor: 5000 }], evidence: [], provenance: { source: "manual" }, created_by: { kind: "user", id: userID }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" };
const integrationConnection = { id: "24000000-0000-4000-8000-000000000024", account_id: accountID, name: "Policy research", kind: "web_research", state: "active", current_revision_id: "25000000-0000-4000-8000-000000000025", current_revision: 2, credential_id: "26000000-0000-4000-8000-000000000026", credential_generation: 1, version: 4, created_by: { user_id: userID }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" };
const integrationHealth = { id: "27000000-0000-4000-8000-000000000027", account_id: accountID, connection_id: integrationConnection.id, connection_revision_id: integrationConnection.current_revision_id, credential_id: integrationConnection.credential_id, state: "healthy", latency_milliseconds: 81, checked_at: "2026-08-24T00:00:00Z" };
const integrationDetail = { connection: integrationConnection, revision: { id: integrationConnection.current_revision_id, account_id: accountID, connection_id: integrationConnection.id, revision: 2, capabilities: ["web.research"], scope: { https_origin: "https://example.com", path_prefix: "/policy" }, created_by: { user_id: userID }, created_at: "2026-08-24T00:00:00Z" }, latest_health: integrationHealth };
const integrationExecution = { id: "28000000-0000-4000-8000-000000000028", account_id: accountID, release_id: "29000000-0000-4000-8000-000000000029", release_version: 3, approval_id: "31000000-0000-4000-8000-000000000031", capability: "web.publish", connection_id: integrationConnection.id, connection_revision_id: integrationConnection.current_revision_id, connection_revision: 2, credential_id: integrationConnection.credential_id, credential_generation: 1, payload_sha256: "e".repeat(64), state: "manual_resolution", attempt_count: 1, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" };
const integrationExecutionDetail = { execution: integrationExecution, attempts: [], resolution: { id: "32000000-0000-4000-8000-000000000032", execution_id: integrationExecution.id, requested_outcome: "succeeded", evidence_sha256: "f".repeat(64), requested_by_user_id: userID, requested_at: "2026-08-24T00:00:00Z", state: "pending" } };
const marketingCampaign = { id: "33000000-0000-4000-8000-000000000033", account_id: accountID, name: "Launch", objective: "Explain the operating model", audience: "Business owners", channels: ["email", "web"], state: "draft", version: 4, created_by: { kind: "user", id: userID }, provenance: { origin: "human" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T01:00:00Z" };
const marketingAsset = { id: "34000000-0000-4000-8000-000000000034", account_id: accountID, campaign_id: marketingCampaign.id, asset_id: "35000000-0000-4000-8000-000000000035", revision: 2, kind: "copy", title: "Launch copy", media_type: "text/plain", content_reference: "opaque", content_sha256: "1".repeat(64), content_bytes: 120, created_by: { kind: "user", id: userID }, provenance: { origin: "human" }, created_at: "2026-08-24T00:30:00Z" };
const marketingRelease = { id: "36000000-0000-4000-8000-000000000036", account_id: accountID, campaign_id: marketingCampaign.id, campaign_version: 4, name: "Launch release", channels: ["email", "web"], asset_revision_ids: [marketingAsset.id], state: "submitted", version: 2, created_by: { kind: "user", id: userID }, provenance: { origin: "human" }, submitted_by: { kind: "user", id: userID }, created_at: "2026-08-24T00:40:00Z", updated_at: "2026-08-24T00:50:00Z" };

interface SyntheticAPIState {
  readonly analyticsEvents: Array<{ name: string; fields?: Record<string, string> }>;
  readonly unhandled: string[];
  securityReady: boolean;
}

function fulfillJSON(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

function fulfillProblem(route: Route, status: number, code: string, detail: string) {
  return route.fulfill({
    status,
    contentType: "application/problem+json",
    body: JSON.stringify({ type: `https://infiniteocean.net/problems/${code}`, title: "Request denied", status, code, detail })
  });
}

function accountWithPackageModes(mode: "enabled" | "read_only", excluded: ReadonlyArray<string> = []) {
  return {
    ...account,
    entitlements: {
      ...account.entitlements,
      packages: account.entitlements.packages
        .filter((item) => !excluded.includes(item.code))
        .map((item) => ({ ...item, mode }))
    }
  };
}

async function overrideSession(page: Page, selectedAccount: ReturnType<typeof accountWithPackageModes>): Promise<void> {
  await page.route("**/api/v1/session/accounts", async (route) => {
    await fulfillJSON(route, { user_id: userID, selected_account_id: accountID, accounts: [selectedAccount] });
  });
}

async function installSyntheticAPI(page: Page): Promise<SyntheticAPIState> {
  const state: SyntheticAPIState = { analyticsEvents: [], unhandled: [], securityReady: false };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/session/accounts") {
      await fulfillJSON(route, { user_id: userID, selected_account_id: accountID, accounts: [account] });
      return;
    }
    if (path === "/api/v1/privacy/consent") {
      if (request.method() === "PUT") {
        const selection = request.postDataJSON() as { analytics: boolean; marketing: boolean };
        await fulfillJSON(route, { ...consent, ...selection });
      } else await fulfillJSON(route, consent);
      return;
    }
    if (path === "/api/v1/privacy/consent/history") {
      await fulfillJSON(route, { decisions: [] });
      return;
    }
    if (path === "/api/v1/privacy/rights-requests") {
      await fulfillJSON(route, { requests: [] });
      return;
    }
    if (path === "/api/v1/analytics/events") {
      const event = request.postDataJSON() as { name: string; fields?: Record<string, string> };
      state.analyticsEvents.push({ name: event.name, fields: event.fields });
      await fulfillJSON(route, {}, 202);
      return;
    }
    if (path === "/api/v1/identity") {
      await fulfillJSON(route, { user_id: userID, primary_email: "owner@example.com" });
      return;
    }
    if (path === "/api/v1/security-posture") {
      await fulfillJSON(route, {
        passkey_count: 1,
        recovery_codes_configured: state.securityReady,
        recovery_codes_remaining: state.securityReady ? 10 : 0,
        owner_ready: state.securityReady
      });
      return;
    }
    if (path === "/api/v1/passkeys") {
      await fulfillJSON(route, { passkeys: [{
        id: "60000000-0000-4000-8000-000000000006",
        name: "Laptop",
        created_at: "2026-08-24T20:00:00Z",
        backup_eligible: true,
        backed_up: true
      }] });
      return;
    }
    if (path === "/api/v1/recovery-codes") {
      if (request.method() === "POST") {
        state.securityReady = true;
        await fulfillJSON(route, {
          status: { configured: true, version: 1, remaining: 10, created_at: "2026-08-25T12:00:00Z" },
          codes: ["ALPHA-BRAVO", "CHARLIE-DELTA"]
        });
      } else await fulfillJSON(route, {
        configured: state.securityReady,
        version: state.securityReady ? 1 : 0,
        remaining: state.securityReady ? 10 : 0,
        created_at: state.securityReady ? "2026-08-25T12:00:00Z" : undefined
      });
      return;
    }
    if (path === "/api/v1/sessions") {
      await fulfillJSON(route, { sessions: [{
        id: "70000000-0000-4000-8000-000000000007",
        client_label: "Current browser",
        authenticated_at: "2026-08-25T11:00:00Z",
        last_seen_at: "2026-08-25T12:00:00Z",
        expires_at: "2026-09-25T12:00:00Z",
        current: true,
        authentication_method: "passkey",
        authentication_assurance: "phishing_resistant"
      }] });
      return;
    }
    if (path === "/api/v1/security-events") {
      await fulfillJSON(route, { events: [] });
      return;
    }
    if (path === "/api/v1/mcp-grants") {
      await fulfillJSON(route, { grants: [] });
      return;
    }
    if (path.startsWith(`/api/v1/accounts/${accountID}/attention/`)) {
      const items = path.endsWith("/approvals") ? [{
        id: "30000000-0000-4000-8000-000000000003",
        capability: "marketing.release.publish",
        invocation_id: "40000000-0000-4000-8000-000000000004",
        operation_id: "50000000-0000-4000-8000-000000000005",
        policy_version: 2,
        proposer: { kind: "workload", id: "campaign-agent" },
        state: "open",
        version: 4,
        created_at: "2026-08-24T20:00:00Z",
        updated_at: "2026-08-24T20:01:00Z",
        expires_at: "2026-08-25T20:00:00Z"
      }] : [];
      await fulfillJSON(route, { items });
      return;
    }
    if (path === "/api/v1/catalog/public") {
      await fulfillJSON(route, catalog);
      return;
    }
    if (path === `/api/v1/accounts/${accountID}/billing`) {
      await fulfillJSON(route, { has_customer: false, can_manage: true, can_start_checkout: true, subscriptions: [] });
      return;
    }
    if (path === `/api/v1/accounts/${accountID}/membership`) {
      await fulfillJSON(route, { membership: ownerMembership });
      return;
    }
    if (path === `/api/v1/accounts/${accountID}/memberships`) {
      await fulfillJSON(route, { memberships: [ownerMembership, memberMembership] });
      return;
    }
    if (path === `/api/v1/accounts/${accountID}/exports` && request.method() === "GET") {
      await fulfillJSON(route, { exports: [queuedExport] });
      return;
    }
    if (path === "/api/v1/account-closures") {
      await fulfillJSON(route, { account_closures: [] });
      return;
    }
    const workBase = `/api/v1/accounts/${accountID}/work-items`;
    if (path === `${workBase}/summary`) {
      await fulfillJSON(route, { active: 1, in_progress: 1, waiting: 0, urgent: 0, done: 0 });
      return;
    }
    if (path === `${workBase}/${workItem.id}/children`) {
      await fulfillJSON(route, { items: [] });
      return;
    }
    if (path === `${workBase}/${workItem.id}`) {
      await fulfillJSON(route, workItem);
      return;
    }
    if (path === workBase && request.method() === "GET") {
      await fulfillJSON(route, { items: [workItem] });
      return;
    }
    const knowledgeBase = `/api/v1/accounts/${accountID}/knowledge`;
    if (path === `${knowledgeBase}/claims/${knowledgeClaim.id}`) {
      await fulfillJSON(route, knowledgeClaim);
      return;
    }
    if (path === `${knowledgeBase}/claims` && request.method() === "GET") {
      await fulfillJSON(route, { items: [knowledgeClaim] });
      return;
    }
    if (path === `${knowledgeBase}/facts` && request.method() === "GET") {
      await fulfillJSON(route, { items: [knowledgeFact] });
      return;
    }
    if (path === `/api/v1/accounts/${accountID}/baseline-assessments/${baselineID}`) {
      await fulfillJSON(route, baseline);
      return;
    }
    const accountBase = `/api/v1/accounts/${accountID}`;
    if (path === `${accountBase}/agent-boardrooms`) {
      await fulfillJSON(route, { items: [agentRoom] });
      return;
    }
    if (path === `${accountBase}/agent-boardrooms/${agentRoom.id}/personas`) {
      await fulfillJSON(route, { items: [agentPersona] });
      return;
    }
    if (path === `${accountBase}/agent-boardrooms/${agentRoom.id}/conversations`) {
      await fulfillJSON(route, { items: [agentConversation] });
      return;
    }
    if (path === `${accountBase}/agent-conversations/${agentConversation.id}`) {
      await fulfillJSON(route, agentConversation);
      return;
    }
    if (path === `${accountBase}/agent-conversations/${agentConversation.id}/messages`) {
      await fulfillJSON(route, { items: [agentMessage] });
      return;
    }
    if (path === `${accountBase}/agent-runs/${agentRunID}`) {
      await fulfillJSON(route, agentRun);
      return;
    }
    if (path === `${accountBase}/schedules/${schedule.id}`) {
      await fulfillJSON(route, schedule);
      return;
    }
    if (path === `${accountBase}/schedules` && request.method() === "GET") {
      await fulfillJSON(route, { items: [schedule] });
      return;
    }
    const financeBase = `${accountBase}/finance`;
    if (path === `${financeBase}/entries/${financeEntry.id}`) {
      await fulfillJSON(route, financeEntry);
      return;
    }
    if (path === `${financeBase}/ledgers/${financeLedger.id}`) {
      await fulfillJSON(route, financeLedger);
      return;
    }
    if (path === `${financeBase}/ledgers`) {
      await fulfillJSON(route, { items: [financeSummary] });
      return;
    }
    if (path === `${financeBase}/ledgers/${financeLedger.id}/accounts`) {
      await fulfillJSON(route, { items: [] });
      return;
    }
    if (path === `${financeBase}/ledgers/${financeLedger.id}/entries`) {
      await fulfillJSON(route, { items: [financeEntry] });
      return;
    }
    if (path === `${financeBase}/ledgers/${financeLedger.id}/reconciliations`) {
      await fulfillJSON(route, { items: [] });
      return;
    }
    const integrationsBase = `${accountBase}/integrations`;
    if (path === `${integrationsBase}/connections/${integrationConnection.id}`) {
      await fulfillJSON(route, integrationDetail);
      return;
    }
    if (path === `${integrationsBase}/connections/${integrationConnection.id}/health`) {
      await fulfillJSON(route, { items: [integrationHealth] });
      return;
    }
    if (path === `${integrationsBase}/connections` && request.method() === "GET") {
      await fulfillJSON(route, { items: [integrationConnection] });
      return;
    }
    if (path === `${integrationsBase}/executions/${integrationExecution.id}`) {
      await fulfillJSON(route, integrationExecutionDetail);
      return;
    }
    if (path === `${integrationsBase}/executions` && request.method() === "GET") {
      await fulfillJSON(route, { items: [integrationExecution] });
      return;
    }
    const marketingBase = `${accountBase}/marketing`;
    if (path === `${marketingBase}/campaigns/${marketingCampaign.id}/asset-revisions`) {
      await fulfillJSON(route, { items: [marketingAsset] });
      return;
    }
    if (path === `${marketingBase}/campaigns/${marketingCampaign.id}/releases`) {
      await fulfillJSON(route, { items: [marketingRelease] });
      return;
    }
    if (path === `${marketingBase}/campaigns/${marketingCampaign.id}`) {
      await fulfillJSON(route, marketingCampaign);
      return;
    }
    if (path === `${marketingBase}/campaigns` && request.method() === "GET") {
      await fulfillJSON(route, { items: [marketingCampaign] });
      return;
    }
    if (path === `${marketingBase}/releases/${marketingRelease.id}`) {
      await fulfillJSON(route, marketingRelease);
      return;
    }
    if (path === "/api/v1/affiliate") {
      await fulfillJSON(route, {
        enrollment_open: false,
        attribution_enabled: false,
        terms_version: 1,
        rule_version: 1,
        settlement_mode: "unconfigured"
      });
      return;
    }
    state.unhandled.push(`${request.method()} ${path}`);
    await fulfillJSON(route, { title: "Synthetic browser route missing", status: 501 }, 501);
  });
  return state;
}

async function expectAccessible(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(results.violations, results.violations.map((violation) =>
    `${violation.id}: ${violation.help}\n${violation.nodes.map((node) => `  ${node.target.join(" ")} — ${node.failureSummary}`).join("\n")}`
  ).join("\n\n")).toEqual([]);
}

async function expectNoHorizontalOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    document: document.documentElement.scrollWidth
  }));
  expect(dimensions.document, `document width ${dimensions.document}px exceeds ${dimensions.viewport}px viewport`).toBeLessThanOrEqual(dimensions.viewport);
}

let state: SyntheticAPIState;
let browserErrors: string[] = [];
let allowedBrowserErrors: RegExp[] = [];

test.beforeEach(async ({ page }) => {
  state = await installSyntheticAPI(page);
  browserErrors = [];
  allowedBrowserErrors = [];
  page.on("pageerror", (error) => browserErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(message.text());
  });
  test.info().annotations.push({ type: "synthetic-api", description: "No identity, credential, database, provider or deployed environment is used." });
});

test.afterEach(async () => {
  expect(state.unhandled, "every private API call must have an intentional synthetic response").toEqual([]);
  expect(browserErrors.filter((message) => !allowedBrowserErrors.some((pattern) => pattern.test(message))), "the browser emitted unexpected runtime errors").toEqual([]);
});

test("Your Turn renders the owner queue without responsive overflow", async ({ page }, testInfo) => {
  await page.goto("/app/your-turn");
  if (testInfo.project.name === "chromium-reduced-motion") {
    expect(await page.evaluate(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches)).toBe(true);
  }
  await expect(page.getByRole("heading", { level: 1, name: "Your Turn" })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: "marketing.release.publish" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Filter Your Turn queue" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("mobile navigation traps and restores focus", async ({ page }, testInfo) => {
  test.skip(!["chromium-phone", "chromium-reflow"].includes(testInfo.project.name), "compact-navigation interaction contract");
  await page.goto("/app/your-turn");
  const menu = page.getByRole("button", { name: "Open navigation" });
  await menu.click();
  const dialog = page.getByRole("dialog", { name: "Application navigation" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute("aria-modal", "true");
  await expect(dialog.getByRole("button", { name: "Close navigation", exact: true })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.press("Shift+Tab");
  await expect(dialog.locator("#account")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(menu).toBeFocused();
});

test("checkout requires deliberate referral application and remains usable at phone width", async ({ page }) => {
  await page.goto("/app/checkout?offer=team-monthly-v1&ref=IO-PARTNER1");
  await expect(page.getByRole("heading", { level: 1, name: "Review before Stripe." })).toBeVisible();
  await expect(page.getByText("A referral was proposed by your link.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to Stripe" })).toBeDisabled();
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByText(/Referral IO-PARTNER1 will be validated/)).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("GDPR controls expose equal rejection and verified rights boundaries", async ({ page }) => {
  await page.goto("/app/privacy");
  await expect(page.getByRole("heading", { level: 1, name: "Privacy you can act on." })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reject non-essential" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Make a tracked request" })).toBeVisible();
  await page.getByRole("button", { name: "Reject non-essential" }).click();
  await expect(page.getByText("Your privacy preferences were saved.")).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("closed Affiliate launch state makes no unapproved payout promise", async ({ page }) => {
  await page.goto("/app/affiliate");
  await expect(page.getByRole("heading", { level: 1, name: "One identity. One clear ledger." })).toBeVisible();
  await expect(page.getByText("Enrollment cannot open until the release owner approves")).toBeVisible();
  await expect(page.getByText("$10", { exact: false })).toHaveCount(0);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("owner-security onboarding records completion only after authoritative readiness", async ({ page }) => {
  await page.goto("/app/security");
  await expect(page.getByRole("heading", { level: 1, name: "Security follows you" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Setup incomplete" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
  expect(state.analyticsEvents.filter((event) => event.name === "security_enrollment_completed")).toEqual([]);

  await page.getByRole("button", { name: "Create recovery codes" }).click();
  await expect(page.getByRole("heading", { name: "Identity secured" })).toBeVisible();
  await expect(page.getByRole("region", { name: "New recovery codes" })).toContainText("Save these now");
  await expect.poll(() => state.analyticsEvents.filter((event) => event.name === "security_enrollment_completed").length).toBe(1);
  expect(state.analyticsEvents.find((event) => event.name === "security_enrollment_completed")).toEqual({
    name: "security_enrollment_completed",
    fields: { method: "passkey_recovery_codes" }
  });
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Account administration keeps authority, billing, portability, and closure understandable", async ({ page }) => {
  const routes = [
    {
      path: "/app/account",
      heading: "People and authority",
      evidence: ["Invite a teammate", "Morgan Member", "Transfer ownership"]
    },
    {
      path: "/app/billing",
      heading: "Know what the Account pays for",
      evidence: ["Free or unbilled access", "No subscription history", "Review paid plans"]
    },
    {
      path: "/app/account-exports",
      heading: "Take your Account with you",
      evidence: ["Request Account export", "Cancel request", queuedExport.id]
    },
    {
      path: "/app/account-closures",
      heading: "Deliberate and recoverable",
      evidence: ["Request closure", "No closure history", "Closure is not immediate erasure"]
    }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      for (const evidence of route.evidence) await expect(page.getByText(evidence, { exact: false }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("Work, Knowledge, and Baseline preserve governed operating context", async ({ page }) => {
  const routes = [
    {
      path: "/app/work",
      heading: "Work",
      evidence: ["Confirm the launch checklist", "Shared responsibility", "New work"]
    },
    {
      path: `/app/work/${workItem.id}`,
      heading: workItem.title,
      evidence: [workItem.description, "Complete", "Edit responsibility"]
    },
    {
      path: "/app/knowledge",
      heading: "Knowledge",
      evidence: [knowledgeClaim.key, "92% confidence", knowledgeFact.key]
    },
    {
      path: `/app/knowledge/claims/${knowledgeClaim.id}`,
      heading: knowledgeClaim.key,
      evidence: [knowledgeClaim.value, "Launch plan, page 4", "Agent output is never authoritative"]
    },
    {
      path: `/app/baseline/${baselineID}`,
      heading: "Business Baseline",
      evidence: ["What is the legal or registered name", "Confirm my answer now", "Use an existing Knowledge fact"]
    }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      for (const evidence of route.evidence) await expect(page.getByText(evidence, { exact: false }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("package workspaces preserve governed list and durable detail context", async ({ page }) => {
  const routes = [
    { path: "/app/agents", heading: "Agents", evidence: [agentRoom.name, agentRoom.purpose, "New Boardroom"] },
    { path: `/app/agents/boardrooms/${agentRoom.id}`, heading: agentRoom.name, evidence: [agentPersona.name, "Propose only—human approval remains external", "Convene Boardroom"] },
    { path: `/app/agents/boardrooms/${agentRoom.id}/conversations/${agentConversation.id}`, heading: agentRoom.name, evidence: [agentMessage.body, "Security review is open", "Review consequential proposals in Your Turn"] },
    { path: "/app/schedules", heading: "Schedules", evidence: [schedule.name, "Mon at 09:30", "New schedule"] },
    { path: `/app/schedules/${schedule.id}`, heading: schedule.name, evidence: [schedule.timezone, "Run now", "Missed run"] },
    { path: `/app/finance/entries/${financeEntry.id}`, heading: "A governed ledger for operating truth", evidence: ["#8 · Monthly close", "Post entry", "General journal"] },
    { path: `/app/integrations/connections/${integrationConnection.id}`, heading: "Connect deliberately. Observe every effect.", evidence: [integrationConnection.name, "https://example.com/policy", "Healthy"] },
    { path: `/app/integrations/executions/${integrationExecution.id}`, heading: "Connect deliberately. Observe every effect.", evidence: ["web.publish · Manual resolution", "A different owner or administrator must confirm this exact outcome"] },
    { path: `/app/marketing/campaigns/${marketingCampaign.id}`, heading: "Prepare the message. Govern the release.", evidence: [marketingCampaign.name, marketingCampaign.objective, "Revise intent"] },
    { path: `/app/marketing/releases/${marketingRelease.id}`, heading: "Prepare the message. Govern the release.", evidence: [marketingRelease.name, "never accepts an internal approval identifier", "Open Your Turn"] }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      if (route.path === `/app/agents/boardrooms/${agentRoom.id}`) {
        await page.locator("details.agents-persona").first().locator("summary").click();
      }
      for (const evidence of route.evidence) await expect(page.getByText(evidence, { exact: false }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("read-only packages preserve evidence while removing mutation authority", async ({ page }) => {
  await overrideSession(page, accountWithPackageModes("read_only"));
  const routes = [
    {
      path: `/app/work/${workItem.id}`,
      heading: workItem.title,
      evidence: [workItem.description, "read-only access"],
      forbiddenButtons: ["Complete", "Edit responsibility"]
    },
    {
      path: `/app/schedules/${schedule.id}`,
      heading: schedule.name,
      evidence: [schedule.timezone, "read-only access"],
      forbiddenButtons: ["Run now", "Edit definition", "Delete schedule"]
    },
    {
      path: `/app/finance/entries/${financeEntry.id}`,
      heading: "A governed ledger for operating truth",
      evidence: ["#8 · Monthly close", "Read-only access"],
      forbiddenButtons: ["Edit draft", "Post entry"]
    },
    {
      path: `/app/integrations/connections/${integrationConnection.id}`,
      heading: "Connect deliberately. Observe every effect.",
      evidence: [integrationConnection.name, "https://example.com/policy", "Read-only access"],
      forbiddenButtons: ["Revise scope", "Disable", "Revoke connection"]
    },
    {
      path: `/app/marketing/releases/${marketingRelease.id}`,
      heading: "Prepare the message. Govern the release.",
      evidence: [marketingRelease.name, "never accepts an internal approval identifier", "Read-only access"],
      forbiddenButtons: ["Cancel release", "Activate release", "New campaign"]
    }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      for (const evidence of route.evidence) await expect(page.getByText(evidence, { exact: false }).first()).toBeVisible();
      for (const label of route.forbiddenButtons) await expect(page.getByRole("button", { name: label, exact: true })).toHaveCount(0);
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("missing packages expose an explicit upgrade boundary", async ({ page }) => {
  await overrideSession(page, accountWithPackageModes("enabled", ["integrations", "marketing"]));
  const routes = [
    {
      path: `/app/integrations/connections/${integrationConnection.id}`,
      heading: "Integrations is not active",
      evidence: ["Add the Integrations package", "Review Account plans"]
    },
    {
      path: `/app/marketing/releases/${marketingRelease.id}`,
      heading: "Marketing is not included",
      evidence: ["does not expose Marketing", "Review Account billing"]
    }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 2, name: route.heading })).toBeVisible();
      for (const evidence of route.evidence) await expect(page.getByText(evidence, { exact: false }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("the application shell keeps Account-load failure recoverable", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*503/);
  await page.route("**/api/v1/session/accounts", async (route) => {
    await fulfillJSON(route, { title: "Account service temporarily unavailable", status: 503 }, 503);
  });
  await page.goto("/app/your-turn");
  await expect(page.getByRole("status")).toContainText("We could not load your Account.");
  await expect(page.getByRole("link", { name: "Sign in again" })).toHaveAttribute("href", "/login?return_to=%2Fapp");
  await expect(page.locator("#account")).toHaveValue("");
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Work capacity denial preserves the customer's local draft", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*403/);
  await page.route(`**/api/v1/accounts/${accountID}/work-items`, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await fulfillProblem(route, 403, "limit_exceeded", "This Account has reached its active Work limit. Complete or cancel existing Work before trying again.");
  });
  await page.goto("/app/work");
  await page.getByRole("button", { name: "New work" }).click();
  await page.getByLabel("Title").fill("Preserve this capacity-blocked draft");
  await page.getByRole("button", { name: "Create work" }).click();
  await expect(page.getByRole("alert")).toContainText("This Account has reached its active Work limit.");
  await expect(page.getByLabel("Title")).toHaveValue("Preserve this capacity-blocked draft");
  await expect(page.getByRole("button", { name: "Create work" })).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Work conflict reloads authoritative state before retry", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*412/);
  await page.route(`**/api/v1/accounts/${accountID}/work-items/${workItem.id}/transitions`, async (route) => {
    await fulfillProblem(route, 412, "work_version_conflict", "The submitted Work version is stale.");
  });
  await page.goto(`/app/work/${workItem.id}`);
  await page.getByRole("button", { name: "Complete" }).click();
  const dialog = page.getByRole("dialog", { name: "Done this work?" });
  await dialog.getByLabel("Operational reason").fill("The governed launch checklist is complete.");
  await dialog.getByRole("button", { name: "Confirm change" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("alert")).toContainText("Spyglass loaded the current version; review it before trying again.");
  await expect(page.getByRole("heading", { level: 1, name: workItem.title })).toBeVisible();
  await expect(page.getByRole("button", { name: "Complete" })).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Checkout provider failure leaves payment and Account state unchanged", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*502/);
  await page.route(`**/api/v1/accounts/${accountID}/checkout-sessions`, async (route) => {
    await fulfillProblem(route, 502, "billing_provider_unavailable", "Stripe is temporarily unavailable. No payment was started and this Account is unchanged.");
  });
  await page.goto("/app/checkout?offer=team-monthly-v1");
  await page.getByRole("checkbox", { name: /I confirm this offer/ }).check();
  await page.getByRole("button", { name: "Continue to Stripe" }).click();
  await expect(page.getByRole("alert")).toContainText("No payment was started and this Account is unchanged.");
  await expect(page).toHaveURL(/\/app\/checkout\?offer=team-monthly-v1$/);
  await expect(page.getByRole("button", { name: "Continue to Stripe" })).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("multi-Account switching adopts only the server-confirmed context", async ({ page }) => {
  const selections: string[] = [];
  await page.route("**/api/v1/session/accounts", async (route) => {
    await fulfillJSON(route, { user_id: userID, selected_account_id: accountID, accounts: [account, secondAccount] });
  });
  await page.route("**/api/v1/session/account", async (route) => {
    const input = route.request().postDataJSON() as { account_id: string };
    selections.push(input.account_id);
    await fulfillJSON(route, {
      account_context: {
        account_id: secondAccountID,
        account_name: secondAccount.display_name,
        cell_id: secondAccount.cell_id,
        entitlement_version: secondAccount.entitlements.version,
        placement_generation: secondAccount.placement_generation,
        role: secondAccount.role
      }
    });
  });
  await page.goto("/app/privacy");
  const menu = page.getByRole("button", { name: "Open navigation" });
  const compact = await menu.isVisible();
  if (compact) await menu.click();
  await page.locator("#account").selectOption(secondAccountID);
  await expect(page.locator("#account")).toHaveValue(secondAccountID);
  expect(selections).toEqual([secondAccountID]);
  if (compact) await page.getByRole("dialog", { name: "Application navigation" }).getByRole("button", { name: "Close navigation", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Privacy you can act on." })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("failed multi-Account switching restores the prior Account", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*503/);
  await page.route("**/api/v1/session/accounts", async (route) => {
    await fulfillJSON(route, { user_id: userID, selected_account_id: accountID, accounts: [account, secondAccount] });
  });
  await page.route("**/api/v1/session/account", async (route) => {
    await fulfillProblem(route, 503, "account_context_unavailable", "That Account could not be selected right now. Your current Account is unchanged.");
  });
  await page.goto("/app/privacy");
  const menu = page.getByRole("button", { name: "Open navigation" });
  const compact = await menu.isVisible();
  if (compact) await menu.click();
  await page.locator("#account").selectOption(secondAccountID);
  await expect(page.locator("#account")).toHaveValue(accountID);
  if (compact) await page.getByRole("dialog", { name: "Application navigation" }).getByRole("button", { name: "Close navigation", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Your current Account is unchanged.");
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Marketing conflict reloads the authoritative campaign before retry", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*409/);
  const revisions: Array<{ body: unknown; version: string | undefined }> = [];
  await page.route(`**/api/v1/accounts/${accountID}/marketing/campaigns/${marketingCampaign.id}`, async (route) => {
    if (route.request().method() === "PUT") {
      revisions.push({ body: route.request().postDataJSON(), version: route.request().headers()["if-match"] });
      await fulfillProblem(route, 409, "marketing_campaign_conflict", "The campaign changed after this form was opened.");
    } else await fulfillJSON(route, marketingCampaign);
  });
  await page.goto(`/app/marketing/campaigns/${marketingCampaign.id}`);
  await page.getByRole("button", { name: "Revise intent" }).click();
  const dialog = page.getByRole("dialog", { name: "Revise campaign" });
  await dialog.getByLabel("Name").fill("Stale launch draft");
  await dialog.getByRole("button", { name: "Revise campaign" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("alert")).toContainText("This Marketing record changed. Review the current version before trying again.");
  await expect(page.getByRole("heading", { level: 2, name: marketingCampaign.name })).toBeVisible();
  expect(revisions).toEqual([{
    body: { name: "Stale launch draft", objective: marketingCampaign.objective, audience: marketingCampaign.audience, channels: marketingCampaign.channels },
    version: `W/"${marketingCampaign.version}"`
  }]);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Integration research failure retains the scoped customer query", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*503/);
  const searches: unknown[] = [];
  await page.route(`**/api/v1/accounts/${accountID}/integrations/web-research/search`, async (route) => {
    searches.push(route.request().postDataJSON());
    await fulfillProblem(route, 503, "integration_provider_unavailable", "Scoped research is temporarily unavailable. Try this exact query again later.");
  });
  await page.goto("/app/integrations");
  await page.getByRole("tab", { name: "Research" }).click();
  const connection = page.getByLabel("Research connection");
  const query = page.getByLabel("Question or search terms");
  await expect(connection).toHaveValue(integrationConnection.id);
  await query.fill("current policy retention requirements");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Scoped research is temporarily unavailable. Try this exact query again later.");
  await expect(query).toHaveValue("current policy retention requirements");
  await expect(connection).toHaveValue(integrationConnection.id);
  expect(searches).toEqual([{ connection_id: integrationConnection.id, query: "current policy retention requirements", limit: 10 }]);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("GDPR rights requests are tracked, deduplicated, and cancelable", async ({ page }) => {
  const submissions: unknown[] = [];
  const cancellations: string[] = [];
  await page.route("**/api/v1/privacy/rights-requests", async (route) => {
    if (route.request().method() === "POST") {
      submissions.push(route.request().postDataJSON());
      await fulfillJSON(route, privacyRightsRequest, 201);
    } else await fulfillJSON(route, { requests: [] });
  });
  await page.route(`**/api/v1/privacy/rights-requests/${privacyRightsRequest.request_id}`, async (route) => {
    cancellations.push(route.request().method());
    await fulfillJSON(route, { ...privacyRightsRequest, state: "canceled", updated_at: "2026-08-25T12:05:00Z" });
  });
  await page.goto("/app/privacy");
  await page.getByLabel("What would you like to do?").selectOption("erasure");
  await page.getByLabel("Which records?").selectOption("affiliate");
  await page.getByRole("button", { name: "Submit verified request" }).click();
  await expect(page.getByRole("status")).toContainText("Your Erasure request for Affiliate data was received.");
  const historyItem = page.getByRole("listitem").filter({ hasText: "Erasure · Affiliate" });
  await expect(historyItem).toContainText("Submitted");
  await expect(page.getByRole("button", { name: "Submit verified request" })).toBeDisabled();
  await historyItem.getByRole("button", { name: "Cancel" }).click();
  await expect(historyItem).toContainText("Canceled");
  await expect(page.getByRole("button", { name: "Submit verified request" })).toBeEnabled();
  expect(submissions).toEqual([{ kind: "erasure", scope: "affiliate" }]);
  expect(cancellations).toEqual(["DELETE"]);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("GDPR rights requests hand off exact context for passkey confirmation", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*403/);
  await page.route("**/api/v1/privacy/rights-requests", async (route) => {
    if (route.request().method() === "POST") {
      await fulfillProblem(route, 403, "strong_reauthentication_required", "Confirm this privacy-rights request with a passkey.");
    } else await fulfillJSON(route, { requests: [] });
  });
  await page.goto("/app/privacy");
  await page.getByLabel("What would you like to do?").selectOption("portability");
  await page.getByLabel("Which records?").selectOption("account");
  await page.getByRole("button", { name: "Submit verified request" }).click();
  await expect(page).toHaveURL(/\/app\/security\?return_to=%2Fapp%2Fprivacy&status=strong_reauthentication_required$/);
  await expect(page.getByRole("heading", { level: 1, name: "Security follows you" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});
