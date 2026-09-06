import { expect, test, type Route } from "@playwright/test";
import { baselinePersonaDescription, baselinePersonaInstructions, baselineRoomPurpose } from "../../apps/app/src/baseline-interviewer";
import { accountID, userID, account, secondAccountID, secondAccount, consent, privacyRightsRequest, terminalPrivacyRightsRequests, ownerMembership, memberMembership, queuedExport, workItem, knowledgeClaim, baselineID, agentRoom, agentPersona, agentConversation, agentMessage, schedule, financeEntry, integrationConnection, integrationDetail, integrationExecution, marketingCampaign, marketingRelease, type SyntheticAPIState, fulfillJSON, fulfillProblem, accountWithPackageModes, overrideSession, installSyntheticAPI, expectAccessible, expectNoHorizontalOverflow } from "./private-fixtures";

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
  await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Filter Your Turn queue" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Your Turn preserves the queue offline and refreshes after reconnect", async ({ page, context }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*ERR_INTERNET_DISCONNECTED/);
  allowedBrowserErrors.push(/Failed to load resource: WebKit encountered an internal error/);
  await page.goto("/app/your-turn");
  await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  const attentionPattern = `**/api/v1/accounts/${accountID}/attention/**`;
  const abortAttention = (route: Route) => route.abort("internetdisconnected");
  await page.route(attentionPattern, abortAttention);
  await context.setOffline(true);
  await expect(page.getByRole("status").filter({ hasText: "You are offline" })).toBeVisible();
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("alert")).toContainText("Your Turn is unavailable right now. Showing the last loaded queue.");
  await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();

  await page.unroute(attentionPattern, abortAttention);
  await context.setOffline(false);
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect(page.getByRole("status").filter({ hasText: "You are offline" })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator(".your-turn > .sr-only")).toHaveText("Your Turn refreshed. 1 open item.");
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Your Turn session expiry preserves the exact sign-in return route", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*401/);
  await page.goto("/app/your-turn");
  await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  await page.route(`**/api/v1/accounts/${accountID}/attention/**`, async (route) => {
    await fulfillProblem(route, 401, "authentication_required", "Sign in again.");
  });
  await page.route("**/login?**", async (route) => {
    await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><html lang=\"en\"><title>Sign in</title><body><main><h1>Sign in again</h1></main></body></html>" });
  });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page).toHaveURL("http://127.0.0.1:4173/login?return_to=%2Fapp%2Fyour-turn");
  await expect(page.getByRole("heading", { level: 1, name: "Sign in again" })).toBeVisible();
});

test("affiliate checkout sign-in preserves the proposal without analytics consent", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*401/);
  await page.route("**/api/v1/privacy/consent", async (route) => {
    await fulfillJSON(route, { ...consent, analytics: false, decided: true });
  });
  await page.route("**/api/v1/session/accounts", async (route) => {
    await fulfillProblem(route, 401, "authentication_required", "Sign in again.");
  });
  await page.route("**/login?**", async (route) => {
    await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><html lang=\"en\"><title>Sign in</title><body><main><h1>Sign in again</h1></main></body></html>" });
  });

  await page.goto("/app/checkout?offer=team-monthly-v2&ref=IO-PARTNER1");

  await expect(page).toHaveURL("http://127.0.0.1:4173/login?return_to=%2Fapp%2Fcheckout%3Foffer%3Dteam-monthly-v2%26ref%3DIO-PARTNER1");
  await expect(page.getByRole("heading", { level: 1, name: "Sign in again" })).toBeVisible();
  expect(state.analyticsEvents).toEqual([]);
});

test("Your Turn sends only one decision while approval is pending", async ({ page }) => {
  const approvalID = "30000000-0000-4000-8000-000000000003";
  const approvalDetail = {
    kind: "approval",
    id: approvalID,
    capability: "marketing.release.publish",
    payload: { release_id: "redacted-release-reference" },
    evidence_sha256: "a".repeat(64),
    input_sha256: "b".repeat(64),
    hash_version: 1,
    invocation_id: "40000000-0000-4000-8000-000000000004",
    operation_id: "50000000-0000-4000-8000-000000000005",
    policy_version: 2,
    proposer: { kind: "workload", id: "campaign-agent" },
    require_independent_review: false,
    state: "open",
    version: 4,
    created_at: "2026-08-24T20:00:00Z",
    updated_at: "2026-08-24T20:01:00Z",
    expires_at: "2026-08-25T20:00:00Z"
  };
  let decisionRequests = 0;
  let releaseDecision!: () => void;
  const decisionReleased = new Promise<void>((resolve) => { releaseDecision = resolve; });
  await page.route(`**/api/v1/accounts/${accountID}/attention/approvals/${approvalID}**`, async (route) => {
    if (route.request().method() === "GET") {
      await fulfillJSON(route, approvalDetail);
      return;
    }
    decisionRequests += 1;
    await decisionReleased;
    await fulfillJSON(route, { ...approvalDetail, state: "approved", version: 5 });
  });

  await page.goto(`/app/your-turn/approval/${approvalID}`);
  await page.getByRole("radio", { name: "Approve this action" }).check();
  await page.getByLabel("Reason").fill("The governed release is ready.");
  await page.getByRole("checkbox", { name: /I reviewed the proposed action/ }).check();
  await page.locator("form.decision-card").evaluate((form) => {
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });

  await expect.poll(() => decisionRequests).toBe(1);
  await expect(page.getByRole("button", { name: "Saving…" })).toBeDisabled();
  await page.getByRole("link", { name: "Back to Your Turn" }).click();
  await expect(page).toHaveURL(new RegExp(`/app/your-turn/approval/${approvalID}$`));
  await expect(page.getByRole("status").filter({ hasText: "This decision is still being saved." })).toBeVisible();
  releaseDecision();
  await expect(page).toHaveURL(/\/app\/your-turn\?completed=approval$/);
  expect(decisionRequests).toBe(1);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Your Turn warns before leaving an unsaved consequential decision", async ({ page }) => {
  const approvalID = "30000000-0000-4000-8000-000000000003";
  await page.route(`**/api/v1/accounts/${accountID}/attention/approvals/${approvalID}**`, async (route) => {
    await fulfillJSON(route, {
      kind: "approval",
      id: approvalID,
      capability: "marketing.release.publish",
      payload: { release_id: "redacted-release-reference" },
      evidence_sha256: "a".repeat(64),
      input_sha256: "b".repeat(64),
      hash_version: 1,
      invocation_id: "40000000-0000-4000-8000-000000000004",
      operation_id: "50000000-0000-4000-8000-000000000005",
      policy_version: 2,
      proposer: { kind: "workload", id: "campaign-agent" },
      require_independent_review: false,
      state: "open",
      version: 4,
      created_at: "2026-08-24T20:00:00Z",
      updated_at: "2026-08-24T20:01:00Z",
      expires_at: "2026-08-25T20:00:00Z"
    });
  });
  await page.goto(`/app/your-turn/approval/${approvalID}`);
  await page.getByRole("radio", { name: "Approve this action" }).check();
  await page.getByLabel("Reason").fill("The governed release is ready after final review.");
  await page.getByRole("checkbox", { name: /I reviewed the proposed action/ }).check();

  const dismissed = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => {
      resolve(dialog.message());
      await dialog.dismiss();
    });
  });
  await page.getByRole("link", { name: "Back to Your Turn" }).click();
  await expect(dismissed).resolves.toBe("Leave this decision? Your draft will remain only in this browser tab until you return.");
  await expect(page).toHaveURL(new RegExp(`/app/your-turn/approval/${approvalID}$`));
  await expect(page.getByLabel("Reason")).toHaveValue("The governed release is ready after final review.");
  await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your decision draft remains" })).toBeVisible();

  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("link", { name: "Back to Your Turn" }).click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("@text-zoom authenticated routes retain content and reflow at 200% text size", async ({ page }) => {
  const routes = [
    { path: "/app/your-turn", heading: "Your Turn" },
    { path: "/app/checkout?offer=team-monthly-v2", heading: "Review before Stripe." },
    { path: "/app/privacy", heading: "Privacy" },
    { path: "/app/affiliate", heading: "Affiliate program" },
    { path: "/app/account", heading: "Account" },
    { path: `/app/work/${workItem.id}`, heading: workItem.title },
    { path: `/app/knowledge/claims/${knowledgeClaim.id}`, heading: "Launch release window" },
    { path: `/app/baseline/${baselineID}`, heading: "Business profile" },
    { path: `/app/agents/boardrooms/${agentRoom.id}/conversations/${agentConversation.id}`, heading: agentConversation.subject },
    { path: `/app/schedules/${schedule.id}`, heading: schedule.name },
    { path: `/app/finance/entries/${financeEntry.id}`, heading: "Finance" },
    { path: `/app/integrations/executions/${integrationExecution.id}`, heading: "Integrations" },
    { path: `/app/marketing/releases/${marketingRelease.id}`, heading: "Marketing" },
    { path: "/app/billing", heading: "Billing" },
    { path: "/app/security", heading: "Security" },
    { path: "/app/account-exports", heading: "Exports" },
    { path: "/app/account-closures", heading: "Close account" }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await page.addStyleTag({ content: "html { font-size: 200% !important; }" });
      await expect.poll(() => page.evaluate(() => Number.parseFloat(getComputedStyle(document.documentElement).fontSize))).toBeGreaterThanOrEqual(32);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("@browser-zoom authenticated routes reflow at 400% browser scale", async ({ page, context }) => {
  const cdp = await context.newCDPSession(page);
  await cdp.send("Emulation.setDeviceMetricsOverride", {
    width: 320,
    height: 225,
    deviceScaleFactor: 4,
    mobile: false,
    screenWidth: 1280,
    screenHeight: 900,
    screenOrientation: { type: "landscapePrimary", angle: 0 }
  });
  await expect.poll(() => page.evaluate(() => ({
    cssWidth: innerWidth,
    cssHeight: innerHeight,
    devicePixelRatio,
    screenWidth: screen.width,
    screenHeight: screen.height
  }))).toEqual({ cssWidth: 320, cssHeight: 225, devicePixelRatio: 4, screenWidth: 1280, screenHeight: 900 });

  const routes = [
    { path: "/app/your-turn", heading: "Your Turn" },
    { path: "/app/checkout?offer=team-monthly-v2", heading: "Review before Stripe." },
    { path: "/app/privacy", heading: "Privacy" },
    { path: "/app/affiliate", heading: "Affiliate program" },
    { path: "/app/account", heading: "Account" },
    { path: `/app/work/${workItem.id}`, heading: workItem.title },
    { path: `/app/knowledge/claims/${knowledgeClaim.id}`, heading: "Launch release window" },
    { path: `/app/baseline/${baselineID}`, heading: "Business profile" },
    { path: `/app/agents/boardrooms/${agentRoom.id}/conversations/${agentConversation.id}`, heading: agentConversation.subject },
    { path: `/app/schedules/${schedule.id}`, heading: schedule.name },
    { path: `/app/finance/entries/${financeEntry.id}`, heading: "Finance" },
    { path: `/app/integrations/executions/${integrationExecution.id}`, heading: "Integrations" },
    { path: `/app/marketing/releases/${marketingRelease.id}`, heading: "Marketing" },
    { path: "/app/billing", heading: "Billing" },
    { path: "/app/security", heading: "Security" },
    { path: "/app/account-exports", heading: "Exports" },
    { path: "/app/account-closures", heading: "Close account" }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("mobile workspace switching focuses chat and restores the working view", async ({ page }, testInfo) => {
  test.skip(!["chromium-phone-360", "chromium-phone", "chromium-phone-412", "chromium-reflow", "chromium-tablet"].includes(testInfo.project.name), "compact workspace interaction");
  await page.goto("/app/your-turn");
  await expect(page.locator("#workspace-message")).toBeAttached();
  await page.getByRole("button", { name: "Chat", exact: true }).click();
  await expect(page.locator("#workspace-message")).toBeFocused();
  await page.getByRole("button", { name: "Back to view", exact: true }).click();
  await expect(page.locator("#main")).toBeFocused();
  await expect(page.getByRole("heading", { level: 1, name: "Your Turn" })).toBeVisible();
});

test("checkout requires deliberate referral application and remains usable at phone width", async ({ page }) => {
  await page.goto("/app/checkout?offer=team-monthly-v2&ref=IO-PARTNER1");
  await expect(page.getByRole("heading", { level: 1, name: "Review before Stripe." })).toBeVisible();
  await expect.poll(() => state.analyticsEvents.find((event) => event.name === "application_entered")?.fields).toEqual({ entry_point: "checkout" });
  await expect(page.getByText("A referral was proposed by your link.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to Stripe" })).toBeDisabled();
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByText(/Referral IO-PARTNER1 will be validated/)).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("checkout keeps optional commissioning explicit and separates one-time from recurring cost", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*502/);
  const requests: Array<{ body: unknown; idempotencyKey: string | null }> = [];
  await page.route(`**/api/v1/accounts/${accountID}/checkout-sessions`, async (route) => {
    requests.push({ body: route.request().postDataJSON(), idempotencyKey: route.request().headers()["idempotency-key"] ?? null });
    await fulfillProblem(route, 502, "billing_unavailable", "Stripe is temporarily unavailable. The reviewed checkout remains unchanged.");
  });
  await page.goto("/app/checkout?offer=team-monthly-v2");
  await expect(page.getByText("$250.00 once.")).toBeVisible();
  await expect(page.getByText(/earns no Affiliate commission/)).toBeVisible();
  await page.getByRole("checkbox", { name: /Add the optional commissioning package/ }).check();
  await expect(page.getByText("$300.00", { exact: true })).toBeVisible();
  await expect(page.getByText(/\$50.00 recurs monthly; commissioning is one time/)).toBeVisible();
  await page.getByRole("checkbox", { name: /I confirm this offer and optional commissioning/ }).check();
  await page.getByRole("button", { name: "Continue to Stripe" }).click();
  await expect(page.getByRole("alert")).toContainText("reviewed checkout remains unchanged");
  expect(requests).toHaveLength(1);
  expect(requests[0]?.body).toEqual({ offer_code: "team-monthly-v2", include_commissioning: true });
  expect(requests[0]?.idempotencyKey).toMatch(/^[0-9a-f-]{36}$/);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Billing exposes governed top-up, promotion, and later commissioning controls", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*502/);
  const purchaseRequests: Array<{ body: unknown; idempotencyKey: string | null }> = [];
  await page.route(`**/api/v1/accounts/${accountID}/billing`, async (route) => {
    await fulfillJSON(route, { has_customer: true, can_manage: true, can_start_checkout: false, commissioning_purchased: false, subscriptions: [{ state: "active", offer_code: "team-monthly-v2", catalog_version: 3, current_period_start: "2026-08-01T00:00:00Z", current_period_end: "2026-09-01T00:00:00Z", last_synced_at: "2026-08-27T00:00:00Z" }] });
  });
  await page.route(`**/api/v1/accounts/${accountID}/purchase-checkout-sessions`, async (route) => {
    purchaseRequests.push({ body: route.request().postDataJSON(), idempotencyKey: route.request().headers()["idempotency-key"] ?? null });
    await fulfillProblem(route, 502, "billing_unavailable", "Stripe purchase checkout is temporarily unavailable.");
  });
  await page.route(`**/api/v1/accounts/${accountID}/ai-token-promotions`, async (route) => {
    await fulfillJSON(route, { grant: { definition_code: "launch_bonus", catalog_version: 3, quantity: 1000, expires_at: "2026-10-01T00:00:00Z", created_at: "2026-08-27T00:00:00Z" }, balance: { available: 1000, reserved: 0, consumed: 0, included: 0, purchased: 0, promotion: 1000 } }, 201);
  });
  await page.goto("/app/billing");
  await expect(page.getByRole("button", { name: "Buy 10,000 AI Tokens for $10.00" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Buy assisted setup" })).toBeVisible();
  await page.getByRole("button", { name: "Buy 10,000 AI Tokens for $10.00" }).click();
  await expect(page.getByRole("alert")).toContainText("purchase checkout is temporarily unavailable");
  expect(purchaseRequests[0]?.body).toEqual({ kind: "ai_token_top_up", item_code: "tokens_10k_v1" });
  expect(purchaseRequests[0]?.idempotencyKey).toMatch(/^[0-9a-f-]{36}$/);
  await page.getByLabel("Promotion code").fill("Launch_Bonus");
  await page.getByRole("button", { name: "Redeem" }).click();
  await expect(page.getByRole("status").filter({ hasText: /1,000 promotional AI Tokens were added/ })).toBeVisible();
  await expect(page.getByText("1,000 available", { exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("GDPR controls expose equal rejection and verified rights boundaries", async ({ page }) => {
  await page.goto("/app/privacy");
  await expect(page.getByRole("heading", { level: 1, name: "Privacy" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reject non-essential" })).toBeVisible();
  await expect(page.getByText("Marketing tracking", { exact: true })).toBeVisible();
  await expect(page.getByText("Not used", { exact: true })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: /Marketing/ })).toHaveCount(0);
  const consentHistory = page.getByRole("region", { name: "Consent history" });
  await expect(consentHistory).toContainText("Analytics accepted");
  await expect(consentHistory).toContainText("Marketing rejected");
  await expect(consentHistory).not.toContainText("12000000-0000-4000-8000-000000000012");
  await expect(consentHistory).not.toContainText("13000000-0000-4000-8000-000000000013");
  await expect(page.getByRole("heading", { name: "Make a tracked request" })).toBeVisible();
  await page.getByRole("button", { name: "Reject non-essential" }).click();
  await expect(page.getByText("Your privacy preferences were saved.")).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Affiliate portability downloads a privacy-safe JSON artifact", async ({ page }) => {
  let exportRequests = 0;
  await page.route("**/api/v1/affiliate/data-export", async (route) => {
    exportRequests += 1;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: {
        "Cache-Control": "no-store",
        "Content-Disposition": "attachment; filename=\"spyglass-affiliate-data.json\""
      },
      body: JSON.stringify({
        schema_version: 1,
        generated_at: "2026-08-26T12:00:00Z",
        public_codes: [],
        enrollment_events: [],
        attribution_summary: { total: 0, reserved: 0, locked: 0, canceled: 0 },
        commission_entries: [],
        support_requests: [],
        support_events: []
      })
    });
  });
  await page.goto("/app/privacy");
  const downloadStarted = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download Affiliate data" }).click();
  const download = await downloadStarted;

  expect(exportRequests).toBe(1);
  expect(download.suggestedFilename()).toMatch(/^spyglass-affiliate-data-\d{4}-\d{2}-\d{2}\.json$/);
  await expect(page.getByRole("status").filter({ hasText: "Affiliate data export download started" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Affiliate portability hands off exact context for passkey confirmation", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*403/);
  await page.route("**/api/v1/affiliate/data-export", async (route) => {
    await fulfillProblem(route, 403, "strong_reauthentication_required", "Confirm this Affiliate export with a passkey.");
  });
  await page.goto("/app/privacy");
  await page.getByRole("button", { name: "Download Affiliate data" }).click();
  await expect(page).toHaveURL(/\/app\/security\?return_to=%2Fapp%2Fprivacy&status=strong_reauthentication_required$/);
  await expect(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("closed Affiliate launch state makes no unapproved payout promise", async ({ page }) => {
  await page.goto("/app/affiliate");
  await expect(page.getByRole("heading", { level: 1, name: "Affiliate program" })).toBeVisible();
  await expect(page.getByText("The affiliate program is not open yet.")).toBeVisible();
  await expect(page.getByText("$10", { exact: false })).toHaveCount(0);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Affiliate dashboard exposes an aggregate renewal ledger and fails closed when suspended", async ({ page }) => {
  let enrollmentState: "active" | "suspended" = "active";
  let publicCode = "IO-PARTNER1";
  let enrollmentVersion = 1;
  const codeReplacements: unknown[] = [];
  let releaseSupport: (() => void) | undefined;
  const supportReleased = new Promise<void>((resolve) => { releaseSupport = resolve; });
  await page.route("**/api/v1/affiliate", async (route) => {
    await fulfillJSON(route, {
      enrollment_open: enrollmentState === "active",
      attribution_enabled: enrollmentState === "active",
      terms_version: 2,
      rule_version: 3,
      settlement_mode: "account_credit",
      enrollment: {
        affiliate_id: "10000000-0000-4000-8000-000000000041",
        user_id: userID,
        public_code: publicCode,
        terms_version: 2,
        rule_version: 3,
        state: enrollmentState,
        version: enrollmentVersion,
        created_at: "2026-08-24T20:00:00Z"
      }
    });
  });
  await page.route("**/api/v1/affiliate/code-replacements", async (route) => {
    codeReplacements.push(route.request().postDataJSON());
    publicCode = "IO-PARTNER2";
    enrollmentVersion += 1;
    await fulfillJSON(route, {
      enrollment_open: true,
      attribution_enabled: true,
      terms_version: 2,
      rule_version: 3,
      settlement_mode: "account_credit",
      enrollment: {
        affiliate_id: "10000000-0000-4000-8000-000000000041",
        user_id: userID,
        public_code: publicCode,
        terms_version: 2,
        rule_version: 3,
        state: "active",
        version: enrollmentVersion,
        created_at: "2026-08-24T20:00:00Z"
      }
    });
  });
  await page.route("**/api/v1/affiliate/statement", async (route) => {
    await fulfillJSON(route, {
      affiliate_id: "10000000-0000-4000-8000-000000000041",
      referred_subscriptions: 3,
      currency: "USD",
      pending_minor: 1000,
      available_minor: 2000,
      reserved_minor: 0,
      settled_minor: 2000,
      reversed_minor: 1000,
      voided_minor: 0,
      check_threshold_minor: 10000,
      check_eligible: false,
      entries: [{
        entry_id: "10000000-0000-4000-8000-000000000042",
        affiliate_id: "10000000-0000-4000-8000-000000000041",
        attribution_id: "10000000-0000-4000-8000-000000000043",
        rule_version: 3,
        cycle: 2,
        kind: "earned",
        state: "pending",
        amount_minor: 1000,
        currency: "USD",
        available_at: "2026-09-24T20:00:00Z",
        created_at: "2026-08-24T20:00:00Z"
      }]
    });
  });
  await page.route("**/api/v1/affiliate/support-requests", async (route) => {
    if (route.request().method() === "POST") {
      await supportReleased;
      await fulfillJSON(route, {
        affiliate_id: "10000000-0000-4000-8000-000000000041",
        created_at: "2026-08-25T12:00:00Z",
        kind: "enrollment_appeal",
        request_id: "10000000-0000-4000-8000-000000000044",
        state: "submitted",
        updated_at: "2026-08-25T12:00:00Z",
        version: 1
      }, 201);
    } else await fulfillJSON(route, { requests: [] });
  });

  await page.goto("/app/affiliate");
  await expect(page.getByRole("heading", { level: 2, name: "IO-PARTNER1" })).toBeVisible();
  await expect(page.getByText("Qualifying cycle 2")).toBeVisible();
  await expect(page.getByRole("group", { name: "Commission totals" })).toContainText("Referred subscriptions3");
  await expect(page.getByRole("heading", { level: 3, name: "August 2026" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Commission totals" })).toContainText("$20.00");
  await expect(page.getByText("customer@example.test", { exact: false })).toHaveCount(0);
  await expect(page.getByText("Customer Company", { exact: false })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Copy code" })).toBeEnabled();
  await expect(page.getByRole("textbox", { name: "Referral link" })).toHaveValue(new URL("/app/checkout?ref=IO-PARTNER1", page.url()).href);
  await expect(page.getByRole("button", { name: "Copy referral link" })).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  await page.getByRole("button", { name: "Replace public code" }).click();
  await expect(page.getByText("every link using it will stop creating future referrals", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Confirm code replacement" }).click();
  await expect(page.getByRole("heading", { level: 2, name: "IO-PARTNER2" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Referral link" })).toHaveValue(new URL("/app/checkout?ref=IO-PARTNER2", page.url()).href);
  await expect(page.getByRole("status").filter({ hasText: "Existing subscription credit is unchanged" })).toBeVisible();
  expect(codeReplacements).toEqual([{ expected_version: 1 }]);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  enrollmentState = "suspended";
  enrollmentVersion += 1;
  await page.reload();
  await expect(page.getByText("Referral attribution is paused for this enrollment.")).toBeVisible();
  await expect(page.getByText("historical commission records remain available below.", { exact: false })).toBeVisible();
  await expect(page.getByText("Qualifying cycle 2")).toBeVisible();
  await expect(page.getByRole("button", { name: "Copy code" })).toBeDisabled();
  await expect(page.getByRole("textbox", { name: "Referral link" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Copy referral link" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Request status review" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  await page.getByRole("button", { name: "Request status review" }).click();
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/affiliate$/);
  await expect(page.getByRole("status").filter({ hasText: "This Affiliate request is still in progress" })).toBeVisible();
  releaseSupport?.();
  await expect(page.getByRole("button", { name: "Review requested" })).toBeDisabled();
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
});

test("checkout keeps the chosen referral visible when self-referral is denied without analytics consent", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*400/);
  const checkoutRequests: unknown[] = [];
  await page.route("**/api/v1/privacy/consent", async (route) => {
    await fulfillJSON(route, { ...consent, analytics: false, decided: true });
  });
  await page.route(`**/api/v1/accounts/${accountID}/checkout-sessions`, async (route) => {
    checkoutRequests.push(route.request().postDataJSON());
    await fulfillProblem(route, 400, "affiliate_self_referral", "An Affiliate cannot refer an Account they own.");
  });

  await page.goto("/app/checkout?offer=team-monthly-v2&ref=IO-PARTNER1");
  await page.getByRole("button", { name: "Apply" }).click();
  await page.getByRole("checkbox", { name: /I confirm this offer and Affiliate referral/ }).check();
  await page.getByRole("button", { name: "Continue to Stripe" }).click();

  await expect(page.getByRole("alert")).toContainText("An Affiliate cannot refer an Account they own.");
  await expect(page.getByRole("textbox", { name: "Affiliate code" })).toHaveValue("IO-PARTNER1");
  await expect(page.getByRole("button", { name: "Remove" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to Stripe" })).toBeEnabled();
  expect(checkoutRequests).toEqual([{ offer_code: "team-monthly-v2", affiliate_code: "IO-PARTNER1" }]);
  expect(state.analyticsEvents).toEqual([]);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("owner-security onboarding records completion only after authoritative readiness", async ({ page }) => {
  await page.goto("/app/security");
  await expect(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Setup incomplete" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
  expect(state.analyticsEvents.filter((event) => event.name === "security_enrollment_completed")).toEqual([]);

  await page.getByRole("button", { name: "Create recovery codes" }).click();
  await expect(page.getByRole("heading", { name: "Sign-in protection is set up" })).toBeVisible();
  await expect(page.getByRole("region", { name: "New recovery codes" })).toContainText("Save these now");
  await expect.poll(() => state.analyticsEvents.filter((event) => event.name === "security_enrollment_completed").length).toBe(1);
  expect(state.analyticsEvents.find((event) => event.name === "security_enrollment_completed")).toEqual({
    name: "security_enrollment_completed",
    fields: { method: "passkey_recovery_codes" }
  });
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  const dismissed = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => {
      resolve(dialog.message());
      await dialog.dismiss();
    });
  });
  await page.locator(".workspace-attention").click();
  await expect(dismissed).resolves.toBe("Leave Security? Your entered values or one-time recovery codes may be lost.");
  await expect(page).toHaveURL(/\/app\/security$/);
  await expect(page.getByRole("region", { name: "New recovery codes" })).toContainText("Save these now");
  await expect(page.getByRole("status").filter({ hasText: "Save every one-time recovery code" })).toBeVisible();

  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
});

test("Account administration keeps authority, billing, portability, and closure understandable", async ({ page }) => {
  const routes = [
    {
      path: "/app/account",
      heading: "Account",
      evidence: ["Invite a teammate", "Morgan Member", "Transfer ownership"]
    },
    {
      path: "/app/billing",
      heading: "Billing",
      evidence: ["Checkout required", "No subscription history", "Review paid plans"]
    },
    {
      path: "/app/account-exports",
      heading: "Exports",
      evidence: ["Request Account export", "Cancel request", queuedExport.id]
    },
    {
      path: "/app/account-closures",
      heading: "Close account",
      evidence: ["Request closure", "No closure history"]
    }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      for (const evidence of route.evidence) await expect(page.getByText(evidence, { exact: false }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);

      if (route.path === "/app/account") {
        const email = page.getByLabel("Email address");
        await email.fill("new.member@example.test");
        const dismissed = new Promise<string>((resolve) => {
          page.once("dialog", async (dialog) => { resolve(dialog.message()); await dialog.dismiss(); });
        });
        await page.locator(".workspace-attention").click();
        await expect(dismissed).resolves.toBe("Leave Account administration? Your invitation or team command will be lost.");
        await expect(page).toHaveURL(/\/app\/account$/);
        await expect(email).toHaveValue("new.member@example.test");
        await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your invitation or team command remains available." })).toBeVisible();
        page.once("dialog", (dialog) => dialog.accept());
        await page.locator(".workspace-attention").click();
        await expect(page).toHaveURL(/\/app\/your-turn$/);
      }

      if (route.path === "/app/account-exports" || route.path === "/app/account-closures") {
        const isExport = route.path === "/app/account-exports";
        await page.getByRole("button", { name: isExport ? "Cancel request" : "Request closure" }).click();
        const dialog = page.getByRole("dialog");
        if (isExport) await dialog.getByLabel("Type CANCEL to confirm").fill("CANCEL");
        else await dialog.getByLabel("Reason for this change").fill("The owner is reviewing this lifecycle change.");
        const dismissed = new Promise<string>((resolve) => {
          page.once("dialog", async (browserDialog) => { resolve(browserDialog.type()); await browserDialog.dismiss(); });
        });
        await page.evaluate(() => history.back());
        await expect(dismissed).resolves.toBe("beforeunload");
        await expect(page).toHaveURL(new RegExp(`${route.path}$`));
        if (isExport) await expect(dialog.getByLabel("Type CANCEL to confirm")).toHaveValue("CANCEL");
        else await expect(dialog.getByLabel("Reason for this change")).toHaveValue("The owner is reviewing this lifecycle change.");
        page.once("dialog", (browserDialog) => browserDialog.accept());
        await page.evaluate(() => history.back());
        await expect(page).toHaveURL(/\/app\/billing$/);
      }
    });
  }
});

test("new Accounts preserve actionable empty states across customer workspaces", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*404/);
  await page.route(`**/api/v1/accounts/${accountID}/baseline-assessments/current`, async (route) => {
    await fulfillProblem(route, 404, "baseline_assessment_not_found", "No Business Baseline has been started.");
  });
  await page.route(`**/api/v1/accounts/${accountID}/finance/ledgers?*`, async (route) => {
    await fulfillJSON(route, { items: [] });
  });
  await page.route(`**/api/v1/accounts/${accountID}/marketing/campaigns?*`, async (route) => {
    await fulfillJSON(route, { items: [] });
  });
  await page.route(`**/api/v1/accounts/${accountID}/integrations/connections?*`, async (route) => {
    await fulfillJSON(route, { items: null });
  });
  await page.route(`**/api/v1/accounts/${accountID}/integrations/executions?*`, async (route) => {
    await fulfillJSON(route, { items: null });
  });

  const routes = [
    { path: "/app/baseline", heading: "Business profile", evidence: "Start the conversation" },
    { path: "/app/finance", heading: "Finance", evidence: "No ledgers yet" },
    { path: "/app/marketing", heading: "Marketing", evidence: "No campaigns yet" },
    { path: "/app/integrations", heading: "Integrations", evidence: "No connections match this view." }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(route.path);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expect(page.getByText(route.evidence, { exact: false }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
});

test("Work and Knowledge preserve governed operating context", async ({ page }) => {
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
      evidence: ["Launch release window", "92% confidence", "Legal name"]
    },
    {
      path: `/app/knowledge/claims/${knowledgeClaim.id}`,
      heading: "Launch release window",
      evidence: [knowledgeClaim.value, "Launch plan, page 4", "Check the information and its sources before saving it."]
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

test("conversational Baseline learns, proposes setup Work, and hands off to Your Turn", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium-desktop", "The stateful journey runs once; component coverage verifies its responsive presentation.");
  allowedBrowserErrors.push(/Failed to load resource:.*404/);
  const roomID = "41000000-0000-4000-8000-000000000041";
  const personaID = "42000000-0000-4000-8000-000000000042";
  const personaVersionID = "43000000-0000-4000-8000-000000000043";
  const conversationID = "44000000-0000-4000-8000-000000000044";
  const approvedMessageID = "46000000-0000-4000-8000-000000000046";
  const commandTrail: string[] = [];
  let runNumber = 1;
  let factNumber = 0;
  let approvedWork: Record<string, unknown> | undefined;
  const room = {
    id: roomID, account_id: accountID, manager_persona_id: personaID, name: "Business Setup", purpose: baselineRoomPurpose,
    state: "active", version: 2, created_at: "2026-09-03T12:00:00Z", updated_at: "2026-09-03T12:00:00Z"
  };
  const persona = {
    id: personaID, boardroom_id: roomID, state: "active", latest_version: 1, persona_version_id: personaVersionID,
    name: "Operations Guide", role: "Main operations agent", description: baselinePersonaDescription,
    system_instructions: baselinePersonaInstructions, content_digest: "a".repeat(64),
    policy: { complexity: "balanced", maximum_input_tokens: 24000, maximum_output_tokens: 4096, maximum_cost_micros: 250000, maximum_tool_steps: 0, citation_policy: "none", action_policy: "none", tools: [], output_schema: {} },
    created_at: "2026-09-03T12:00:00Z", updated_at: "2026-09-03T12:00:00Z"
  };
  const interview = (overrides: Record<string, unknown> = {}) => ({
    business_type: "Commercial HVAC service company", business_type_confidence: "high",
    captured_topics: ["Commercial HVAC repair"], next_question_key: "baseline.lead_intake",
    next_question: "How does a new service call reach you today?", question_reason: "This shows how demand becomes scheduled work.",
    automation_offers: [], approved_work: [], ready: false, readiness_reason: "I am still learning how work moves.",
    missing_topics: ["Scheduling", "Billing", "Customer follow-up"], ...overrides
  });
  const result = (contribution: string, baselineResult: Record<string, unknown>) => ({
    contribution, findings: [], recommendations: [], questions: [], citations: [], proposed_actions: [], delegations: [], confidence: "medium", baseline: baselineResult
  });
  const messages: Array<Record<string, unknown>> = [
    { id: "47000000-0000-4000-8000-000000000047", conversation_id: conversationID, sequence: 1, role: "user", body: "Please begin my Business Baseline interview.", created_by: userID, created_at: "2026-09-03T12:00:00Z" },
    { id: "48000000-0000-4000-8000-000000000048", conversation_id: conversationID, sequence: 2, role: "persona", body: "I’ll learn how the shop runs and remember what matters. How does a new service call reach you today?", run_id: "49000000-0000-4000-8000-000000000049", invocation_id: "4a000000-0000-4000-8000-00000000004a", persona_version_id: personaVersionID, result: result("How does a new service call reach you today?", interview()), created_at: "2026-09-03T12:00:01Z" }
  ];
  const conversation = () => ({ id: conversationID, boardroom_id: roomID, subject: `Business Baseline · ${baselineID}`, state: "open", message_count: messages.length, created_by: userID, created_at: "2026-09-03T12:00:00Z", updated_at: "2026-09-03T12:00:01Z" });
  const run = (id: string, prompt: string) => ({ id, boardroom_id: roomID, conversation_id: conversationID, state: "succeeded", mode: "selected", subject: conversation().subject, prompt, user_message_id: crypto.randomUUID(), turns: [], invocation_ids: [], invocations: [], resolutions: [], context: [], context_digest: "b".repeat(64), entitlement_version: 3, policy_version: 1, plan_digest: "c".repeat(64), created_at: "2026-09-03T12:00:02Z" });
  const work = (version: number, linked: boolean, assigned: boolean) => ({
    id: approvedMessageID, number: 81, depth: 0, kind: "todo", title: "Set up a daily dispatch review",
    description: "Choose the schedule source and decide who handles exceptions.\n\nThis is approved setup Work from the Business Baseline. Work on this task rather than continuing the onboarding interview.",
    state: "open", priority: "high", assignment: assigned ? { responsibility: "persona", persona_id: personaID } : { responsibility: "shared" },
    provenance: linked ? { source: "conversation", created_by: { kind: "user", id: userID }, conversation_id: conversationID } : { source: "manual", created_by: { kind: "user", id: userID } },
    version, created_at: "2026-09-03T12:00:03Z", updated_at: "2026-09-03T12:00:03Z"
  });

  await page.route(`**/api/v1/accounts/${accountID}/**`, async (route) => {
    const request = route.request(); const path = new URL(request.url()).pathname;
    const accountBase = `/api/v1/accounts/${accountID}`;
    const boardrooms = `${accountBase}/agent-boardrooms`;
    const knowledge = `${accountBase}/knowledge`;
    const workItems = `${accountBase}/work-items`;
    if (path === boardrooms && request.method() === "GET") { await fulfillJSON(route, { items: [room] }); return; }
    if (path === `${boardrooms}/${roomID}/personas` && request.method() === "GET") { await fulfillJSON(route, { items: [persona] }); return; }
    if (path === `${boardrooms}/${roomID}/conversations` && request.method() === "GET") { await fulfillJSON(route, { items: [conversation()] }); return; }
    if (path === `${accountBase}/agent-conversations/${conversationID}/messages` && request.method() === "GET") { await fulfillJSON(route, { items: messages }); return; }
    if (path.startsWith(`${accountBase}/agent-runs/`) && request.method() === "GET") { await fulfillJSON(route, run(path.split("/").at(-1)!, "Previous interview turn")); return; }
    if (path === `${boardrooms}/${roomID}/runs` && request.method() === "POST") {
      const input = request.postDataJSON() as { prompt: string; conversation_id?: string; context: { knowledge_fact_ids: string[] } };
      expect(input.conversation_id).toBe(conversationID); commandTrail.push(`run:${input.prompt}`); runNumber += 1;
      const runID = `50000000-0000-4000-8000-${String(runNumber).padStart(12, "0")}`;
      messages.push({ id: crypto.randomUUID(), conversation_id: conversationID, sequence: messages.length + 1, role: "user", body: input.prompt, created_by: userID, created_at: "2026-09-03T12:01:00Z" });
      if (runNumber === 2) {
        messages.push({ id: "51000000-0000-4000-8000-000000000051", conversation_id: conversationID, sequence: messages.length + 1, role: "persona", body: "That whiteboard is doing too much. I can add a small daily dispatch setup job if you want it.", run_id: runID, invocation_id: crypto.randomUUID(), persona_version_id: personaVersionID, result: result("That whiteboard is doing too much. I can add a small daily dispatch setup job if you want it.", interview({ captured_topics: ["Commercial HVAC repair", "Calls arrive by phone and are scheduled on a whiteboard"], next_question_key: "baseline.dispatch_owner", next_question: "Who decides which technician gets each call?", automation_offers: [{ key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source and decide who handles exceptions.", reason: "Calls currently depend on a whiteboard." }] })), created_at: "2026-09-03T12:01:01Z" });
      } else if (runNumber === 3) {
        messages.push({ id: approvedMessageID, conversation_id: conversationID, sequence: messages.length + 1, role: "persona", body: "I added that setup job. Who decides which technician gets each call?", run_id: runID, invocation_id: crypto.randomUUID(), persona_version_id: personaVersionID, result: result("I added that setup job. Who decides which technician gets each call?", interview({ captured_topics: ["Commercial HVAC repair", "Calls arrive by phone and are scheduled on a whiteboard"], next_question_key: "baseline.dispatch_owner", next_question: "Who decides which technician gets each call?", approved_work: [{ key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source and decide who handles exceptions.", priority: "high" }] })), created_at: "2026-09-03T12:02:01Z" });
      } else {
        messages.push({ id: "52000000-0000-4000-8000-000000000052", conversation_id: conversationID, sequence: messages.length + 1, role: "persona", body: "I have enough to get started. I’ll send anything that needs your judgment to Your Turn.", run_id: runID, invocation_id: crypto.randomUUID(), persona_version_id: personaVersionID, result: result("I have enough to get started. I’ll send anything that needs your judgment to Your Turn.", interview({ captured_topics: ["Commercial HVAC repair", "Lead intake", "Dispatch ownership", "Scheduling tools"], next_question_key: "", next_question: "", question_reason: "", automation_offers: [], approved_work: [], ready: true, readiness_reason: "I understand how calls enter the business, get assigned, and become scheduled work.", missing_topics: [] })), created_at: "2026-09-03T12:03:01Z" });
      }
      await fulfillJSON(route, run(runID, input.prompt), 201); return;
    }
    if (path === `${knowledge}/facts` && request.method() === "GET") { await fulfillJSON(route, { items: [] }); return; }
    if (path === `${knowledge}/evidence` && request.method() === "POST") {
      const input = request.postDataJSON() as { source_kind: string; source_reference: string };
      expect(input.source_kind).toBe("owner_statement"); expect(input.source_reference).toContain(`baseline/${baselineID}/conversation/${conversationID}/baseline.`);
      factNumber += 1; commandTrail.push("knowledge:evidence"); await fulfillJSON(route, { id: `53000000-0000-4000-8000-${String(factNumber).padStart(12, "0")}` }, 201); return;
    }
    if (path === `${knowledge}/claims` && request.method() === "POST") {
      const input = request.postDataJSON() as { key: string; value: string }; commandTrail.push(`knowledge:${input.key}`);
      await fulfillJSON(route, { id: `54000000-0000-4000-8000-${String(factNumber).padStart(12, "0")}`, account_id: accountID, scope: { kind: "account" }, key: input.key, value: input.value, value_sha256: "d".repeat(64), hash_version: 1, confidence: 1000, sensitivity: "internal", citations: [], state: "proposed", proposed_by: { kind: "user", id: userID }, version: 1, created_at: "2026-09-03T12:01:00Z", updated_at: "2026-09-03T12:01:00Z" }, 201); return;
    }
    if (path.startsWith(`${knowledge}/claims/`) && path.endsWith("/decisions") && request.method() === "POST") {
      const key = factNumber === 1 ? "baseline.lead_intake" : "baseline.dispatch_owner"; commandTrail.push("knowledge:accepted");
      await fulfillJSON(route, { claim: { id: path.split("/").at(-2), version: 2 }, fact: { id: `55000000-0000-4000-8000-${String(factNumber).padStart(12, "0")}`, account_id: accountID, current_claim_id: path.split("/").at(-2), scope: { kind: "account" }, key, sensitivity: "internal", state: "active", revision: 1, accepted_by_user_id: userID, accepted_at: "2026-09-03T12:01:00Z", created_at: "2026-09-03T12:01:00Z", updated_at: "2026-09-03T12:01:00Z" } }); return;
    }
    if (path === workItems && request.method() === "GET") { await fulfillJSON(route, { items: approvedWork ? [approvedWork] : [] }); return; }
    if (path === workItems && request.method() === "POST") {
      expect(request.headers()["idempotency-key"]).toBe(approvedMessageID); commandTrail.push("work:created");
      const input = request.postDataJSON() as { assignment: { responsibility: string; persona_id: string } };
      expect(input.assignment).toEqual({ responsibility: "persona", persona_id: personaID });
      approvedWork ??= work(1, false, true); await fulfillJSON(route, approvedWork, 201); return;
    }
    if (path === `${workItems}/${approvedMessageID}` && request.method() === "GET") {
      if (approvedWork) await fulfillJSON(route, approvedWork);
      else await fulfillProblem(route, 404, "work_item_not_found", "Work item not found.");
      return;
    }
    await route.fallback();
  });

  await page.goto(`/app/baseline/${baselineID}`);
  await expect(page.getByText("How does a new service call reach you today?", { exact: false }).last()).toBeVisible();
  await page.getByLabel("Your reply").fill("Calls come from Google and go onto a whiteboard.");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByRole("heading", { level: 2, name: "Suggested tasks" })).toBeVisible();
  await page.getByRole("button", { name: "Yes, add this" }).click();
  await expect(page.getByRole("link", { name: "Set up a daily dispatch review" })).toBeVisible();
  await page.getByLabel("Your reply").fill("I assign calls in the morning; my lead technician handles emergencies.");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByRole("heading", { level: 2, name: "Business setup complete" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to Your Turn" })).toBeVisible();
  expect(commandTrail).toEqual([
    "knowledge:evidence", "knowledge:baseline.lead_intake", "knowledge:accepted", "run:Calls come from Google and go onto a whiteboard.",
    "run:Yes, add “Set up a daily dispatch review” to the work we will set up.", "work:created",
    "knowledge:evidence", "knowledge:baseline.dispatch_owner", "knowledge:accepted", "run:I assign calls in the morning; my lead technician handles emergencies."
  ]);
  await expectNoHorizontalOverflow(page); await expectAccessible(page);
});

test("package workspaces preserve governed list and durable detail context", async ({ page }) => {
  const routes = [
    { path: "/app/agents", heading: "Agents & tools", evidence: [agentRoom.name, agentRoom.purpose, "New agent team"] },
    { path: `/app/agents/boardrooms/${agentRoom.id}`, heading: agentRoom.name, evidence: [agentPersona.name, "Ask for approval first", "Send"] },
    { path: `/app/agents/boardrooms/${agentRoom.id}/conversations/${agentConversation.id}`, heading: agentConversation.subject, evidence: [agentMessage.body, "Security review is open", "Review proposed actions"] },
    { path: "/app/schedules", heading: "Schedules", evidence: [schedule.name, "Mon at 09:30", "New schedule"] },
    { path: `/app/schedules/${schedule.id}`, heading: schedule.name, evidence: [schedule.timezone, "Run now", "Missed run"] },
    { path: `/app/finance/entries/${financeEntry.id}`, heading: "Finance", evidence: ["#8 · Monthly close", "Post entry"] },
    { path: `/app/integrations/connections/${integrationConnection.id}`, heading: "Integrations", evidence: [integrationConnection.name, "https://example.com/policy", "Healthy"] },
    { path: `/app/integrations/executions/${integrationExecution.id}`, heading: "Integrations", evidence: ["web.publish · Manual resolution", "A different owner or administrator must confirm this exact outcome"] },
    { path: `/app/marketing/campaigns/${marketingCampaign.id}`, heading: "Marketing", evidence: [marketingCampaign.name, marketingCampaign.objective, "Edit campaign"] },
    { path: `/app/marketing/releases/${marketingRelease.id}`, heading: "Marketing", evidence: [marketingRelease.name, "Waiting for an owner or administrator to review this release in Your Turn.", "Open Your Turn"] }
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
      heading: "Finance",
      evidence: ["#8 · Monthly close", "Read-only access"],
      forbiddenButtons: ["Edit draft", "Post entry"]
    },
    {
      path: `/app/integrations/connections/${integrationConnection.id}`,
      heading: "Integrations",
      evidence: [integrationConnection.name, "https://example.com/policy", "Read-only access"],
      forbiddenButtons: ["Edit connection", "Disable", "Revoke connection"]
    },
    {
      path: `/app/marketing/releases/${marketingRelease.id}`,
      heading: "Marketing",
      evidence: [marketingRelease.name, "Waiting for an owner or administrator to review this release in Your Turn.", "Read-only access"],
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
  await expect(page.locator(".session-notice")).toContainText("We could not load your account.");
  await expect(page.getByRole("link", { name: "Sign in again" })).toHaveAttribute("href", "/login?return_to=%2Fapp");
  await expect(page.locator("#account")).toHaveValue("");
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Work capacity denial preserves the customer's local draft", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*403/);
  let createRequests = 0;
  let releaseCreate!: () => void;
  const createHeld = new Promise<void>((resolve) => { releaseCreate = resolve; });
  await page.route(`**/api/v1/accounts/${accountID}/work-items`, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    createRequests += 1;
    await createHeld;
    await fulfillProblem(route, 403, "limit_exceeded", "This Account has reached its active Work limit. Complete or cancel existing Work before trying again.");
  });
  await page.goto("/app/work");
  await page.getByRole("button", { name: "New work" }).click();
  await page.getByLabel("Title").fill("Preserve this capacity-blocked draft");
  await page.getByRole("button", { name: "Create work" }).click();
  await expect.poll(() => createRequests).toBe(1);
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/work$/);
  await expect(page.getByRole("status").filter({ hasText: "This Work change is still being saved." })).toBeVisible();
  expect(createRequests).toBe(1);

  releaseCreate();
  await expect(page.getByRole("alert")).toContainText("This Account has reached its active Work limit.");
  await expect(page.getByLabel("Title")).toHaveValue("Preserve this capacity-blocked draft");
  await expect(page.getByRole("button", { name: "Create work" })).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  const dismissed = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => {
      resolve(dialog.message());
      await dialog.dismiss();
    });
  });
  await page.locator(".workspace-attention").click();
  await expect(dismissed).resolves.toBe("Leave Work? Your unsubmitted changes will remain only in this browser tab until you return.");
  await expect(page).toHaveURL(/\/app\/work$/);
  await expect(page.getByLabel("Title")).toHaveValue("Preserve this capacity-blocked draft");
  await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your Work changes remain" })).toBeVisible();

  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
});

test("Work conflict reloads authoritative state before retry", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*412/);
  await page.route(`**/api/v1/accounts/${accountID}/work-items/${workItem.id}/transitions`, async (route) => {
    await fulfillProblem(route, 412, "work_version_conflict", "The submitted Work version is stale.");
  });
  await page.goto(`/app/work/${workItem.id}`);
  await page.getByRole("button", { name: "Complete" }).click();
  const dialog = page.getByRole("dialog", { name: "Complete this task?" });
  await dialog.getByLabel("Reason for this change").fill("The governed launch checklist is complete.");
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
  await page.goto("/app/checkout?offer=team-monthly-v2");
  await page.getByRole("checkbox", { name: /I confirm this offer/ }).check();
  await page.getByRole("button", { name: "Continue to Stripe" }).click();
  await expect(page.getByRole("alert")).toContainText("No payment was started and this Account is unchanged.");
  await expect(page).toHaveURL(/\/app\/checkout\?offer=team-monthly-v2$/);
  await expect(page.getByRole("button", { name: "Continue to Stripe" })).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  const dismissed = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => { resolve(dialog.message()); await dialog.dismiss(); });
  });
  await page.locator(".workspace-attention").click();
  await expect(dismissed).resolves.toBe("Leave checkout? Your reviewed offer, Affiliate code, or confirmation will be lost.");
  await expect(page).toHaveURL(/\/app\/checkout\?offer=team-monthly-v2$/);
  await expect(page.getByRole("checkbox", { name: /I confirm this offer/ })).toBeChecked();
  await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your checkout review remains available." })).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
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
  for (const path of ["agent-boardrooms", "attention/information-requests", "attention/work-reviews"]) {
    await page.route("**/api/v1/accounts/" + secondAccountID + "/" + path + "**", route => fulfillJSON(route, { items: [] }));
  }
  await page.goto("/app/privacy");
  await page.locator("#account").selectOption(secondAccountID);
  await expect(page.locator("#account")).toHaveValue(secondAccountID);
  expect(selections).toEqual([secondAccountID]);
  await expect(page).toHaveURL(/\/app\/workspace$/);
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
  await page.locator("#account").selectOption(secondAccountID);
  await expect(page.locator("#account")).toHaveValue(accountID);
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
  await page.getByRole("button", { name: "Edit campaign" }).click();
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

test("Membership conflict reloads the authoritative team before retry", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*409/);
  const mutations: unknown[] = [];
  let rosterLoads = 0;
  await page.route(`**/api/v1/accounts/${accountID}/memberships`, async (route) => {
    rosterLoads += 1;
    await fulfillJSON(route, { memberships: [ownerMembership, memberMembership] });
  });
  await page.route(`**/api/v1/accounts/${accountID}/memberships/${memberMembership.membership_id}`, async (route) => {
    mutations.push(route.request().postDataJSON());
    await fulfillProblem(route, 409, "membership_version_conflict", "The Membership changed.");
  });
  await page.goto("/app/account");
  const member = page.getByRole("listitem").filter({ hasText: memberMembership.display_name });
  await member.getByRole("button", { name: "Change role" }).click();
  await page.getByRole("dialog").getByLabel("New role").selectOption("administrator");
  await page.getByRole("dialog").getByLabel("Reason for this change").fill("Responsibilities changed during review.");
  await page.getByRole("dialog").getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("alert")).toContainText("This Membership changed. Review the current team before trying again.");
  expect(mutations).toEqual([{ expected_version: memberMembership.version, role: "administrator", reason: "Responsibilities changed during review." }]);
  expect(rosterLoads).toBe(2);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("Schedule conflict reloads the authoritative definition before retry", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*409/);
  const commands: unknown[] = [];
  let detailLoads = 0;
  await page.route(`**/api/v1/accounts/${accountID}/schedules/${schedule.id}`, async (route) => {
    detailLoads += 1;
    await fulfillJSON(route, schedule);
  });
  await page.route(`**/api/v1/accounts/${accountID}/schedules/${schedule.id}/pauses`, async (route) => {
    commands.push(route.request().postDataJSON());
    await fulfillProblem(route, 409, "schedule_version_conflict", "The schedule changed.");
  });
  await page.goto(`/app/schedules/${schedule.id}`);
  await page.getByRole("button", { name: "Pause", exact: true }).click();
  await page.getByRole("dialog").getByLabel("Reason for this change").fill("Pause during launch review.");
  await page.getByRole("dialog").getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("alert")).toContainText("This schedule changed. Review the current version before trying again.");
  expect(commands).toEqual([{ expected_version: schedule.version, reason: "Pause during launch review." }]);
  expect(detailLoads).toBe(2);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("package workspaces reload authoritative state after stale writes", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*409/, /Failed to load resource:.*412/);

  await test.step("Knowledge retains the decision reason while refreshing the exact claim", async () => {
    let conflicted = false;
    let detailLoads = 0;
    const currentClaim = { ...knowledgeClaim, value: "September 2", version: knowledgeClaim.version + 1 };
    await page.route(`**/api/v1/accounts/${accountID}/knowledge/claims/${knowledgeClaim.id}**`, async (route) => {
      if (route.request().method() === "GET") {
        detailLoads += 1;
        await fulfillJSON(route, conflicted ? currentClaim : knowledgeClaim);
        return;
      }
      conflicted = true;
      await fulfillProblem(route, 412, "knowledge_claim_conflict", "The submitted Knowledge claim version is stale.");
    });

    await page.goto(`/app/knowledge/claims/${knowledgeClaim.id}`);
    await page.getByLabel("Reason").fill("The cited launch plan was superseded during review.");
    await page.getByRole("button", { name: "Confirm information" }).click();
    await expect(page.getByRole("alert")).toContainText("This claim changed. Review the current version before deciding again.");
    await expect(page.getByLabel("Reason")).toHaveValue("The cited launch plan was superseded during review.");
    await expect(page.locator(".knowledge-value")).toContainText("September 2");
    await expect(page.locator(".knowledge-detail-card dl div").filter({ hasText: "Version" })).toContainText(String(currentClaim.version));
    await expect(page.getByRole("button", { name: "Confirm information" })).toBeEnabled();
    expect(detailLoads).toBeGreaterThanOrEqual(2);
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);

    const dismissed = new Promise<string>((resolve) => {
      page.once("dialog", async (dialog) => {
        resolve(dialog.message());
        await dialog.dismiss();
      });
    });
    await page.getByRole("link", { name: "Back to Knowledge" }).click();
    await expect(dismissed).resolves.toBe("Leave this Knowledge decision? Your unsubmitted reason will remain only on this page.");
    await expect(page).toHaveURL(new RegExp(`/app/knowledge/claims/${knowledgeClaim.id}$`));
    await expect(page.getByLabel("Reason")).toHaveValue("The cited launch plan was superseded during review.");
    await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your Knowledge decision remains" })).toBeVisible();

    page.once("dialog", (dialog) => dialog.accept());
    await page.getByRole("link", { name: "Back to Knowledge" }).click();
    await expect(page).toHaveURL(/\/app\/knowledge$/);
  });

  await test.step("Agent Persona publication refreshes the immutable published version", async () => {
    let conflicted = false;
    let personaLoads = 0;
    const currentPersona = {
      ...agentPersona,
      latest_version: agentPersona.latest_version + 1,
      persona_version_id: "14000000-0000-4000-8000-000000000114"
    };
    await page.route(`**/api/v1/accounts/${accountID}/agent-boardrooms/${agentRoom.id}/personas`, async (route) => {
      if (route.request().method() === "GET") {
        personaLoads += 1;
        await fulfillJSON(route, { items: [conflicted ? currentPersona : agentPersona] });
        return;
      }
      conflicted = true;
      await fulfillProblem(route, 409, "agent_persona_conflict", "The Persona changed after this editor was opened.");
    });

    await page.goto(`/app/agents/boardrooms/${agentRoom.id}`);
    const persona = page.locator(".agents-persona").filter({ hasText: agentPersona.name });
    await persona.locator("summary").click();
    await persona.getByRole("button", { name: "Save new version" }).click();
    const dialog = page.getByRole("dialog", { name: `Publish ${agentPersona.name} version ${agentPersona.latest_version + 1}` });
    await dialog.getByRole("button", { name: "Save new version" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("alert")).toContainText("This agent changed. Review its current version before saving again.");
    await expect(persona.locator("summary")).toContainText(`version ${currentPersona.latest_version}`);
    await expect(persona.getByRole("button", { name: "Save new version" })).toBeEnabled();
    expect(personaLoads).toBeGreaterThanOrEqual(2);
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);
  });

  await test.step("Finance posting refreshes the current journal entry", async () => {
    let conflicted = false;
    let detailLoads = 0;
    const currentEntry = { ...financeEntry, description: "Monthly close (updated)", version: financeEntry.version + 1 };
    await page.route(`**/api/v1/accounts/${accountID}/finance/entries/${financeEntry.id}`, async (route) => {
      detailLoads += 1;
      await fulfillJSON(route, conflicted ? currentEntry : financeEntry);
    });
    await page.route(`**/api/v1/accounts/${accountID}/finance/entries/${financeEntry.id}/postings`, async (route) => {
      conflicted = true;
      await fulfillProblem(route, 409, "finance_entry_conflict", "The journal entry changed before posting.");
    });

    await page.goto(`/app/finance/entries/${financeEntry.id}`);
    await page.getByRole("button", { name: "Post entry" }).click();
    const dialog = page.getByRole("dialog", { name: "Post entry" });
    await dialog.getByLabel("Type POST to confirm").fill("POST");
    await dialog.getByRole("button", { name: "Post entry" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("alert")).toContainText("This Finance record changed. Review its current version before trying again.");
    await expect(page.getByRole("heading", { level: 2, name: "#8 · Monthly close (updated)" })).toBeVisible();
    await expect(page.locator(".finance-detail dl div").filter({ hasText: "Version" })).toContainText(String(currentEntry.version));
    await expect(page.getByRole("button", { name: "Post entry" })).toBeEnabled();
    expect(detailLoads).toBeGreaterThanOrEqual(2);
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);
  });

  await test.step("Integration commands refresh the current connection state", async () => {
    let conflicted = false;
    let detailLoads = 0;
    const currentConnection = {
      ...integrationConnection,
      name: "Policy research updated",
      version: integrationConnection.version + 1
    };
    await page.route(`**/api/v1/accounts/${accountID}/integrations/connections/${integrationConnection.id}`, async (route) => {
      detailLoads += 1;
      await fulfillJSON(route, conflicted ? {
        ...integrationDetail,
        connection: currentConnection
      } : integrationDetail);
    });
    await page.route(`**/api/v1/accounts/${accountID}/integrations/connections/${integrationConnection.id}/disables`, async (route) => {
      conflicted = true;
      await fulfillProblem(route, 409, "integration_connection_conflict", "The connection changed before it could be disabled.");
    });

    await page.goto(`/app/integrations/connections/${integrationConnection.id}`);
    await page.getByRole("button", { name: "Disable", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Disable connection" });
    await dialog.getByRole("button", { name: "Disable connection" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("alert")).toContainText("This Integration record changed. Review its current state before trying again.");
    await expect(page.getByRole("heading", { level: 2, name: currentConnection.name })).toBeVisible();
    await expect(page.getByRole("button", { name: "Disable", exact: true })).toBeEnabled();
    expect(detailLoads).toBeGreaterThanOrEqual(2);
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);
  });
});

test("package workspaces preserve customer intent through capacity and downstream failures", async ({ page }) => {
  allowedBrowserErrors.push(/Failed to load resource:.*429/, /Failed to load resource:.*503/);

  await test.step("Agent capacity denial retains the exact Boardroom request", async () => {
    const runs: unknown[] = [];
    await page.route(`**/api/v1/accounts/${accountID}/agent-boardrooms/${agentRoom.id}/runs`, async (route) => {
      runs.push(route.request().postDataJSON());
      await fulfillProblem(route, 429, "agent_run_capacity", "Agent Run capacity is temporarily unavailable. Wait for an active run to finish, then try this request again.");
    });

    await page.goto(`/app/agents/boardrooms/${agentRoom.id}`);
    await page.getByRole("button", { name: "Open chat with this team" }).click();
    await page.getByRole("button", { name: "History", exact: true }).click();
    await page.getByLabel("Conversation title").fill("Capacity-safe launch review");
    await page.getByLabel("Message", { exact: true }).fill("Which launch constraint needs attention first?");
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.getByRole("alert")).toContainText("Wait for an active run to finish");
    await expect(page.getByLabel("Conversation title")).toHaveValue("Capacity-safe launch review");
    await expect(page.getByLabel("Message", { exact: true })).toHaveValue("Which launch constraint needs attention first?");
    await expect(page.getByRole("button", { name: "Send" })).toBeEnabled();
    expect(runs).toEqual([{
      mode: "selected",
      persona_ids: [agentPersona.id],
      prompt: "Which launch constraint needs attention first?",
      subject: "Capacity-safe launch review"
    }]);
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);

    await page.getByRole("link", { name: "Settings", exact: true }).click();
    await expect(page).toHaveURL(/\/app\/settings$/);
    await expect(page.getByLabel("Conversation title")).toHaveValue("Capacity-safe launch review");
    await expect(page.getByLabel("Message", { exact: true })).toHaveValue("Which launch constraint needs attention first?");
    await page.getByRole("link", { name: "Close working view" }).click();
    await expect(page).toHaveURL(/\/app\/workspace$/);
    await expect(page.getByLabel("Message", { exact: true })).toHaveValue("Which launch constraint needs attention first?");
  });

  await test.step("Knowledge decision failure retains the reviewed reason", async () => {
    await page.route(`**/api/v1/accounts/${accountID}/knowledge/claims/${knowledgeClaim.id}/decisions`, async (route) => {
      await fulfillProblem(route, 503, "knowledge_temporarily_unavailable", "Knowledge could not save this decision. Review the same claim and try again.");
    });

    await page.goto(`/app/knowledge/claims/${knowledgeClaim.id}`);
    await page.getByLabel("Reason").fill("The cited launch plan remains the reviewed source.");
    await page.getByRole("button", { name: "Confirm information" }).click();
    await expect(page.getByRole("alert")).toContainText("Knowledge could not save this decision");
    await expect(page.getByLabel("Reason")).toHaveValue("The cited launch plan remains the reviewed source.");
    await expect(page.getByRole("button", { name: "Confirm information" })).toBeEnabled();
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);
    page.once("dialog", (dialog) => dialog.accept());
    await page.getByRole("link", { name: "Back to Knowledge" }).click();
    await expect(page).toHaveURL(/\/app\/knowledge$/);
  });

  await test.step("Finance failure keeps the exact posting confirmation", async () => {
    await page.route(`**/api/v1/accounts/${accountID}/finance/entries/${financeEntry.id}/postings`, async (route) => {
      await fulfillProblem(route, 503, "finance_temporarily_unavailable", "Finance could not post this entry. Nothing was posted; confirm and try again.");
    });

    await page.goto("/app/your-turn");
    await page.goto(`/app/finance/entries/${financeEntry.id}`);
    await page.getByRole("button", { name: "Post entry" }).click();
    const dialog = page.getByRole("dialog", { name: "Post entry" });
    await dialog.getByLabel("Type POST to confirm").fill("POST");
    await dialog.getByRole("button", { name: "Post entry" }).click();
    await expect(dialog.getByRole("alert")).toContainText("Nothing was posted");
    await expect(dialog.getByLabel("Type POST to confirm")).toHaveValue("POST");
    await expect(dialog.getByRole("button", { name: "Post entry" })).toBeEnabled();
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);

    const dismissed = new Promise<string>((resolve) => {
      page.once("dialog", async (browserDialog) => {
        resolve(browserDialog.type());
        await browserDialog.dismiss();
      });
    });
    await page.evaluate(() => history.back());
    await expect(dismissed).resolves.toBe("beforeunload");
    await expect(page).toHaveURL(new RegExp(`/app/finance/entries/${financeEntry.id}$`));
    await expect(dialog.getByLabel("Type POST to confirm")).toHaveValue("POST");

    page.once("dialog", (browserDialog) => browserDialog.accept());
    await page.evaluate(() => history.back());
    await expect(page).toHaveURL(/\/app\/your-turn$/);
    await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  });

  await test.step("Marketing failure retains revised campaign intent", async () => {
    await page.route(`**/api/v1/accounts/${accountID}/marketing/campaigns/${marketingCampaign.id}`, async (route) => {
      if (route.request().method() === "PUT") {
        await fulfillProblem(route, 503, "marketing_temporarily_unavailable", "Marketing could not revise this campaign. The draft remains available for retry.");
      } else await fulfillJSON(route, marketingCampaign);
    });

    await page.goto("/app/your-turn");
    await page.goto(`/app/marketing/campaigns/${marketingCampaign.id}`);
    await page.getByRole("button", { name: "Edit campaign" }).click();
    const dialog = page.getByRole("dialog", { name: "Revise campaign" });
    await dialog.getByLabel("Name").fill("Launch readiness follow-up");
    await dialog.getByRole("button", { name: "Revise campaign" }).click();
    await expect(dialog.getByRole("alert")).toContainText("The draft remains available for retry");
    await expect(dialog.getByLabel("Name")).toHaveValue("Launch readiness follow-up");
    await expect(dialog.getByRole("button", { name: "Revise campaign" })).toBeEnabled();
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);

    const dismissed = new Promise<string>((resolve) => {
      page.once("dialog", async (browserDialog) => {
        resolve(browserDialog.type());
        await browserDialog.dismiss();
      });
    });
    await page.evaluate(() => history.back());
    await expect(dismissed).resolves.toBe("beforeunload");
    await expect(page).toHaveURL(new RegExp(`/app/marketing/campaigns/${marketingCampaign.id}$`));
    await expect(dialog.getByLabel("Name")).toHaveValue("Launch readiness follow-up");

    page.once("dialog", (browserDialog) => browserDialog.accept());
    await page.evaluate(() => history.back());
    await expect(page).toHaveURL(/\/app\/your-turn$/);
    await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  });

  await test.step("Integration failure leaves the reviewed command ready to retry", async () => {
    await page.route(`**/api/v1/accounts/${accountID}/integrations/connections/${integrationConnection.id}/disables`, async (route) => {
      await fulfillProblem(route, 503, "integration_temporarily_unavailable", "Integrations could not disable this connection. Its state is unchanged; try again.");
    });

    await page.goto("/app/your-turn");
    await page.goto(`/app/integrations/connections/${integrationConnection.id}`);
    await page.getByRole("button", { name: "Disable", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Disable connection" });
    await dialog.getByRole("button", { name: "Disable connection" }).click();
    await expect(dialog.getByRole("alert")).toContainText("Its state is unchanged");
    await expect(dialog.getByRole("button", { name: "Disable connection" })).toBeEnabled();
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);

    const dismissed = new Promise<string>((resolve) => {
      page.once("dialog", async (browserDialog) => {
        resolve(browserDialog.type());
        await browserDialog.dismiss();
      });
    });
    await page.evaluate(() => history.back());
    await expect(dismissed).resolves.toBe("beforeunload");
    await expect(page).toHaveURL(new RegExp(`/app/integrations/connections/${integrationConnection.id}$`));
    await expect(dialog.getByRole("button", { name: "Disable connection" })).toBeEnabled();

    page.once("dialog", (browserDialog) => browserDialog.accept());
    await page.evaluate(() => history.back());
    await expect(page).toHaveURL(/\/app\/your-turn$/);
    await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  });

  await test.step("Schedule failure exposes the error inside the retry dialog", async () => {
    await page.route(`**/api/v1/accounts/${accountID}/schedules/${schedule.id}/pauses`, async (route) => {
      await fulfillProblem(route, 503, "schedule_temporarily_unavailable", "Schedules could not pause this definition. It remains active; try again.");
    });

    await page.goto(`/app/schedules/${schedule.id}`);
    await page.getByRole("button", { name: "Pause", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Pause this schedule?" });
    await dialog.getByLabel("Reason for this change").fill("Pause while provider health is reviewed.");
    await dialog.getByRole("button", { name: "Confirm" }).click();
    await expect(dialog.getByRole("alert")).toContainText("It remains active");
    await expect(dialog.getByLabel("Reason for this change")).toHaveValue("Pause while provider health is reviewed.");
    await expect(dialog.getByRole("button", { name: "Confirm" })).toBeEnabled();
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);

    const dismissed = new Promise<string>((resolve) => {
      page.once("dialog", async (browserDialog) => {
        resolve(browserDialog.type());
        await browserDialog.dismiss();
      });
    });
    await page.evaluate(() => history.back());
    await expect(dismissed).resolves.toBe("beforeunload");
    await expect(page).toHaveURL(new RegExp(`/app/schedules/${schedule.id}$`));
    await expect(dialog.getByLabel("Reason for this change")).toHaveValue("Pause while provider health is reviewed.");

    page.once("dialog", (browserDialog) => browserDialog.accept());
    await page.evaluate(() => history.back());
    await expect(page).toHaveURL(/\/app\/your-turn$/);
    await expect(page.getByRole("heading", { level: 2, name: "marketing release publish" })).toBeVisible();
  });
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

  const dismissed = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => {
      resolve(dialog.message());
      await dialog.dismiss();
    });
  });
  await page.locator(".workspace-attention").click();
  await expect(dismissed).resolves.toBe("Leave Integrations? Your open command or unsubmitted research query will be lost.");
  await expect(page).toHaveURL(/\/app\/integrations$/);
  await expect(query).toHaveValue("current policy retention requirements");
  await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your Integration command or research query remains available." })).toBeVisible();

  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
});

test("GDPR rights requests are tracked, deduplicated, and cancelable", async ({ page }) => {
  const submissions: unknown[] = [];
  const cancellations: string[] = [];
  let rightsReads = 0;
  await page.route("**/api/v1/privacy/rights-requests", async (route) => {
    if (route.request().method() === "POST") {
      submissions.push(route.request().postDataJSON());
      await fulfillJSON(route, privacyRightsRequest, 201);
    } else {
      rightsReads += 1;
      await fulfillJSON(route, { requests: rightsReads === 1 ? [] : terminalPrivacyRightsRequests });
    }
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
  await page.getByRole("button", { name: "Refresh request status" }).click();
  await expect(page.getByRole("status")).toContainText("Your privacy request status is up to date.");
  await expect(page.getByRole("listitem").filter({ hasText: "Access · Identity" })).toContainText("A privacy reviewer is working on this request.");
  await expect(page.getByRole("listitem").filter({ hasText: "Restriction · Analytics" })).toContainText("Partial resolution recorded.");
  await expect(page.getByRole("listitem").filter({ hasText: "Objection · Affiliate" })).toContainText("Decline recorded.");
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  const analytics = page.getByLabel("Analytics");
  await analytics.uncheck();
  const dismissed = new Promise<string>((resolve) => {
    page.once("dialog", async (dialog) => { resolve(dialog.message()); await dialog.dismiss(); });
  });
  await page.locator(".workspace-attention").click();
  await expect(dismissed).resolves.toBe("Leave Privacy? Your unsaved consent choice or browser-erasure confirmation will be lost.");
  await expect(page).toHaveURL(/\/app\/privacy$/);
  await expect(analytics).not.toBeChecked();
  await expect(page.getByRole("status").filter({ hasText: "Navigation canceled. Your Privacy choices remain available." })).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
});

test("browser privacy erasure is single-flight and blocks navigation until confirmed", async ({ page }) => {
  let releaseErasure: (() => void) | undefined;
  const erasureReleased = new Promise<void>((resolve) => { releaseErasure = resolve; });
  let erasureRequests = 0;
  await page.route("**/api/v1/privacy/data", async (route) => {
    erasureRequests += 1;
    await erasureReleased;
    await route.fulfill({ status: 204 });
  });

  await page.goto("/app/privacy");
  await page.getByRole("button", { name: "Erase browser privacy data" }).click();
  await page.getByRole("button", { name: "Confirm browser-data erasure" }).click();
  const pending = page.getByRole("button", { name: "Erasing browser data…" });
  await expect(pending).toBeDisabled();
  await pending.evaluate((button: HTMLButtonElement) => button.click());
  expect(erasureRequests).toBe(1);

  await page.locator(".workspace-attention").click();
  await expect(page).toHaveURL(/\/app\/privacy$/);
  await expect(page.getByRole("status").filter({ hasText: "This Privacy request is still in progress" })).toBeVisible();

  releaseErasure?.();
  await expect(page.getByRole("status").filter({ hasText: "privacy receipt and raw analytics were erased" })).toBeVisible();
  expect(erasureRequests).toBe(1);
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
  await expect(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});
