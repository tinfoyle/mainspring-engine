import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page, type Route } from "@playwright/test";

interface PublicAPIState {
  readonly analyticsEvents: Array<{ name: string; fields?: Record<string, string> }>;
  readonly unhandled: string[];
}

const undecidedConsent = {
  analytics: false,
  decided: false,
  marketing: false,
  policy_version: 1,
  renewal_required: false,
  surface: "public"
};

const publicFeatureAndPolicyRoutes = [
  { path: "/features", heading: "One operating loop. Clear boundaries." },
  { path: "/features/your-turn", heading: "Decide with context" },
  { path: "/features/work", heading: "Turn gaps into progress" },
  { path: "/features/knowledge", heading: "Build memory with evidence" },
  { path: "/features/baseline", heading: "See what is missing" },
  { path: "/features/agents", heading: "Delegate inside boundaries" },
  { path: "/features/schedules", heading: "Make recurrence explicit" },
  { path: "/features/finance", heading: "Keep records governed" },
  { path: "/features/marketing", heading: "Move from plan to release" },
  { path: "/features/integrations", heading: "Connect without surrendering control" },
  { path: "/features/account-administration", heading: "Keep authority understandable" },
  { path: "/features/security", heading: "Protect consequential authority" },
  { path: "/features/export-lifecycle", heading: "Leave with a traceable boundary" },
  { path: "/privacy", heading: "Privacy, in plain language." },
  { path: "/affiliate-terms", heading: "Affiliate program terms" }
] as const;

function fulfillJSON(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

async function installPublicAPI(page: Page): Promise<PublicAPIState> {
  const analyticsEvents: PublicAPIState["analyticsEvents"] = [];
  const unhandled: string[] = [];
  await page.route("http://127.0.0.1:4174/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/privacy/consent") {
      if (request.method() === "PUT") {
        const selection = request.postDataJSON() as { analytics: boolean; marketing: boolean };
        await fulfillJSON(route, {
          ...undecidedConsent,
          ...selection,
          decided: true,
          decision_id: "10000000-0000-4000-8000-000000000001",
          effective_at: "2026-08-25T12:00:00Z"
        });
      } else await fulfillJSON(route, undecidedConsent);
      return;
    }
    if (path === "/api/v1/analytics/events") {
      const event = request.postDataJSON() as { name: string; fields?: Record<string, string> };
      analyticsEvents.push({ name: event.name, fields: event.fields });
      await fulfillJSON(route, {}, 202);
      return;
    }
    unhandled.push(`${request.method()} ${path}`);
    await fulfillJSON(route, { title: "Synthetic public browser route missing", status: 501 }, 501);
  });
  await page.route("http://127.0.0.1:4173/signup**", (route) => route.fulfill({
    status: 200,
    contentType: "text/html",
    body: "<!doctype html><html lang=en><title>Synthetic signup handoff</title><body><main><h1>Signup handoff received</h1></main></body></html>"
  }));
  return { analyticsEvents, unhandled };
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

let state: PublicAPIState;
let browserErrors: string[] = [];

test.beforeEach(async ({ page }) => {
  state = await installPublicAPI(page);
  browserErrors = [];
  page.on("pageerror", (error) => browserErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(message.text());
  });
});

test.afterEach(() => {
  expect(state.unhandled, "every public browser API call must have an intentional response").toEqual([]);
  expect(browserErrors, "the public browser emitted runtime errors").toEqual([]);
});

test("landing consent gates analytics and preserves the signup handoff", async ({ page }, testInfo) => {
  await page.goto("http://127.0.0.1:4174/");
  await expect(page.getByRole("heading", { level: 1, name: "Know what needs you next." })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Privacy without the fog" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
  expect(state.analyticsEvents).toEqual([]);

  await page.getByRole("button", { name: "Accept analytics" }).click();
  await expect(page.getByRole("button", { name: "Privacy choices" })).toBeFocused();
  expect(state.analyticsEvents).toEqual([]);

  await page.getByRole("link", { name: "Start free", exact: true }).first().click();
  await expect(page).toHaveURL("http://127.0.0.1:4173/signup");
  await expect.poll(() => state.analyticsEvents.length).toBe(2);
  expect(state.analyticsEvents.map((event) => event.name).sort()).toEqual(["primary_cta_selected", "signup_handoff_started"]);
  expect(state.analyticsEvents.find((event) => event.name === "primary_cta_selected")?.fields).toMatchObject(
    ["chromium-phone-360", "chromium-phone", "chromium-phone-412", "chromium-reflow"].includes(testInfo.project.name)
      ? { cta_code: "hero_start_free", route_name: "landing" }
      : { cta_code: "navigation_start_free", route_name: "index" }
  );
});

test("pricing remains usable after rejection and carries only the opaque offer", async ({ page }) => {
  await page.goto("http://127.0.0.1:4174/pricing");
  await expect(page.getByRole("heading", { level: 1, name: /Start free/ })).toBeVisible();
  await expect(page.getByText("$49.00")).toBeVisible();
  await page.getByRole("button", { name: "Reject non-essential" }).click();
  await expect(page.getByRole("button", { name: "Privacy choices" })).toBeFocused();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  await page.getByRole("link", { name: "Choose Team" }).click();
  await expect(page).toHaveURL("http://127.0.0.1:4173/signup?offer=team-monthly-v1");
  expect(state.analyticsEvents).toEqual([]);
});

test("public phone menu contains focus and restores the opener", async ({ page }, testInfo) => {
  test.skip(!["chromium-phone-360", "chromium-phone", "chromium-phone-412", "chromium-reflow"].includes(testInfo.project.name), "compact-navigation interaction contract");
  await page.goto("http://127.0.0.1:4174/");
  const menu = page.getByRole("button", { name: "Menu" });
  await menu.click();
  const dialog = page.getByRole("dialog", { name: "Site menu" });
  await expect(dialog.getByRole("button", { name: "Close site menu" })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(dialog.getByRole("link", { name: "Start free" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(menu).toBeFocused();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("complete feature and policy inventory remains rendered, private, and accessible", async ({ page }) => {
  for (const route of publicFeatureAndPolicyRoutes) {
    await test.step(route.path, async () => {
      await page.goto(`http://127.0.0.1:4174${route.path}`);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expect(page.getByRole("heading", { name: "Privacy without the fog" })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
      expect(state.analyticsEvents, `${route.path} emitted before an analytics decision`).toEqual([]);
    });
  }
});
