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
const consent = {
  analytics: true,
  decided: true,
  marketing: false,
  policy_version: 1,
  renewal_required: false,
  surface: "private"
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

interface SyntheticAPIState {
  readonly analyticsEvents: Array<{ name: string; fields?: Record<string, string> }>;
  readonly unhandled: string[];
  securityReady: boolean;
}

function fulfillJSON(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
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
    if (path === `/api/v1/accounts/${accountID}/integrations/connections` && request.method() === "GET") {
      await fulfillJSON(route, { items: [] });
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

test.beforeEach(async ({ page }) => {
  state = await installSyntheticAPI(page);
  browserErrors = [];
  page.on("pageerror", (error) => browserErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(message.text());
  });
  test.info().annotations.push({ type: "synthetic-api", description: "No identity, credential, database, provider or deployed environment is used." });
});

test.afterEach(async () => {
  expect(state.unhandled, "every private API call must have an intentional synthetic response").toEqual([]);
  expect(browserErrors, "the browser emitted runtime errors").toEqual([]);
});

test("Your Turn renders the owner queue without responsive overflow", async ({ page }) => {
  await page.goto("/app/your-turn");
  await expect(page.getByRole("heading", { level: 1, name: "Your Turn" })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: "marketing.release.publish" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Filter Your Turn queue" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("mobile navigation traps and restores focus", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium-phone", "phone-only interaction contract");
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
