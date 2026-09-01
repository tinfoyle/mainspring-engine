import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page, type Route } from "@playwright/test";

interface PublicAPIState {
  readonly analyticsEvents: Array<{ name: string; fields?: Record<string, string> }>;
  readonly unhandled: string[];
  currentConsent: Record<string, unknown>;
  readonly privacyDecisions: Array<Record<string, unknown>>;
  erasures: number;
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
  { path: "/features", heading: "Keep the whole business in view." },
  { path: "/features/your-turn", heading: "Handle what needs you" },
  { path: "/features/work", heading: "Keep every job moving" },
  { path: "/features/knowledge", heading: "Keep answers and documents together" },
  { path: "/features/baseline", heading: "Get the business out of your head" },
  { path: "/features/agents", heading: "Give AI agents real jobs" },
  { path: "/features/schedules", heading: "Stay ahead of recurring work" },
  { path: "/features/finance", heading: "Keep financial records organized" },
  { path: "/features/marketing", heading: "Plan it, review it, send it" },
  { path: "/features/integrations", heading: "Connect the tools you already use" },
  { path: "/features/account-administration", heading: "Manage your team and access" },
  { path: "/features/security", heading: "Protect the important stuff" },
  { path: "/features/export-lifecycle", heading: "Get your data when you need it" },
  { path: "/privacy", heading: "Privacy, in plain language." },
  { path: "/terms", heading: "Terms and Conditions" },
  { path: "/affiliate-terms", heading: "Affiliate program terms" }
] as const;

function fulfillJSON(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

async function installPublicAPI(page: Page): Promise<PublicAPIState> {
  const state: PublicAPIState = {
    analyticsEvents: [],
    unhandled: [],
    currentConsent: { ...undecidedConsent },
    privacyDecisions: [],
    erasures: 0
  };
  await page.route("http://127.0.0.1:4174/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/privacy/consent") {
      if (request.method() === "PUT") {
        const selection = request.postDataJSON() as { analytics: boolean; marketing: boolean };
        state.currentConsent = {
          ...undecidedConsent,
          ...selection,
          decided: true,
          effective_at: "2026-08-25T12:00:00Z"
        };
        state.privacyDecisions.unshift({
          decision_id: "10000000-0000-4000-8000-000000000001",
          subject_id: "10000000-0000-4000-8000-000000000002",
          policy_version: 1,
          surface: "public",
          ...selection,
          effective_at: "2026-08-25T12:00:00Z"
        });
        await fulfillJSON(route, state.currentConsent);
      } else await fulfillJSON(route, state.currentConsent);
      return;
    }
    if (path === "/api/v1/privacy/consent/history") {
      await fulfillJSON(route, { decisions: state.privacyDecisions });
      return;
    }
    if (path === "/api/v1/privacy/data" && request.method() === "DELETE") {
      state.currentConsent = { ...undecidedConsent };
      state.privacyDecisions.splice(0);
      state.erasures += 1;
      await route.fulfill({ status: 204, body: "" });
      return;
    }
    if (path === "/api/v1/analytics/events") {
      const event = request.postDataJSON() as { name: string; fields?: Record<string, string> };
      state.analyticsEvents.push({ name: event.name, fields: event.fields });
      await fulfillJSON(route, {}, 202);
      return;
    }
    state.unhandled.push(`${request.method()} ${path}`);
    await fulfillJSON(route, { title: "Synthetic public browser route missing", status: 501 }, 501);
  });
  await page.route("http://127.0.0.1:4173/signup**", (route) => route.fulfill({
    status: 200,
    contentType: "text/html",
    body: "<!doctype html><html lang=en><title>Synthetic signup handoff</title><body><main><h1>Signup handoff received</h1></main></body></html>"
  }));
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
    document: document.documentElement.scrollWidth,
    offenders: Array.from(document.querySelectorAll<HTMLElement>("body *")).map((element) => {
      const rect = element.getBoundingClientRect();
      return { selector: `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ""}${Array.from(element.classList).map((name) => `.${name}`).join("")}`, left: Math.round(rect.left), right: Math.round(rect.right), width: Math.round(rect.width), scrollWidth: element.scrollWidth };
    }).filter((element) => element.left < 0 || element.right > document.documentElement.clientWidth || element.scrollWidth > element.width + 1).slice(0, 12)
  }));
  expect(dimensions.document, `document width ${dimensions.document}px exceeds ${dimensions.viewport}px viewport; offenders: ${JSON.stringify(dimensions.offenders)}`).toBeLessThanOrEqual(dimensions.viewport);
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
  await expect(page.locator(".turn-preview")).toHaveCSS("transform", "none");
  await expect(page.getByRole("heading", { name: "Your privacy choices" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
  expect(state.analyticsEvents).toEqual([]);

  if (["chromium-phone-360", "chromium-phone", "chromium-phone-412", "chromium-reflow"].includes(testInfo.project.name)) {
    const layout = await page.evaluate(() => {
      const consent = document.querySelector<HTMLElement>(".consent")?.getBoundingClientRect();
      const actions = document.querySelector<HTMLElement>(".hero__actions")?.getBoundingClientRect();
      return {
        consentHeight: consent?.height ?? Number.POSITIVE_INFINITY,
        consentTop: consent?.top ?? Number.NEGATIVE_INFINITY,
        heroActionsBottom: actions?.bottom ?? Number.POSITIVE_INFINITY
      };
    });
    expect(layout.consentHeight).toBeLessThanOrEqual(300);
    expect(layout.heroActionsBottom).toBeLessThanOrEqual(layout.consentTop);
  }

  await page.getByRole("button", { name: "Manage preferences" }).click();
  await expect(page.getByRole("checkbox", { name: /Analytics/ })).toHaveCount(1);
  await expect(page.getByRole("checkbox", { name: /Marketing/ })).toHaveCount(0);
  await expect(page.getByText("Spyglass does not ask you to consent to one")).toBeVisible();
  await page.getByRole("checkbox", { name: /Analytics/ }).check();
  await page.getByRole("button", { name: "Save preferences" }).click();
  await expect(page.getByRole("button", { name: "Privacy choices" })).toBeFocused();
  expect(state.analyticsEvents).toEqual([]);

  await page.getByRole("link", { name: "Create your team", exact: true }).first().click();
  await expect(page).toHaveURL("http://127.0.0.1:4173/signup?offer=team-monthly-v2");
  await expect.poll(() => state.analyticsEvents.length).toBe(2);
  expect(state.analyticsEvents.map((event) => event.name).sort()).toEqual(["primary_cta_selected", "signup_handoff_started"]);
  expect(state.analyticsEvents.find((event) => event.name === "primary_cta_selected")?.fields).toMatchObject(
    ["chromium-phone-360", "chromium-phone", "chromium-phone-412", "chromium-reflow"].includes(testInfo.project.name)
      ? { cta_code: "hero_create_team", route_name: "landing" }
      : { cta_code: "navigation_create_team", route_name: "index" }
  );
});

test("pricing remains usable after rejection and carries only the opaque offer", async ({ page }) => {
  await page.goto("http://127.0.0.1:4174/pricing");
  await expect(page.getByRole("heading", { level: 1, name: /\$50 a month/ })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: /\$50\.00/ })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: /\$250\.00/ })).toBeVisible();
  await expect(page.getByText(/help you bring in your business information and get Spyglass set up/)).toBeVisible();
  await expect(page.getByRole("link", { name: "Add setup help at checkout" })).toHaveAttribute("href", "http://127.0.0.1:4173/signup?offer=team-monthly-v2");
  await expect(page.getByRole("link", { name: "Contact Support" })).toHaveCount(0);
  await page.getByRole("button", { name: "Reject non-essential" }).click();
  await expect(page.getByRole("button", { name: "Privacy choices" })).toBeFocused();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  await page.getByRole("link", { name: "Create your team", exact: true }).last().click();
  await expect(page).toHaveURL("http://127.0.0.1:4173/signup?offer=team-monthly-v2");
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
  await expect(dialog.getByRole("link", { name: "Create your team" })).toBeFocused();
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
      await expect(page.getByRole("heading", { name: "Your privacy choices" })).toBeVisible();
      if (route.path === "/features") {
        for (const featureRoute of publicFeatureAndPolicyRoutes.filter((candidate) => candidate.path.startsWith("/features/"))) {
          await expect(page.locator(`main a[href="${featureRoute.path}"]`), `${featureRoute.path} must remain reachable from the feature story`).toHaveCount(1);
        }
      }
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
      expect(state.analyticsEvents, `${route.path} emitted before an analytics decision`).toEqual([]);
    });
  }
  await page.goto("http://127.0.0.1:4174/affiliate-terms");
  await expect(page.getByText(/every qualifying paid renewal create a \$10\.00 earning/)).toBeVisible();
  await expect(page.getByText(/At \$100\.00 USD of available credit/)).toBeVisible();
  await expect(page.getByText(/retained for seven years/)).toBeVisible();
});

test("public consent history is inspectable and browser erasure reopens equal choices", async ({ page }) => {
  state.currentConsent = {
    ...undecidedConsent,
    analytics: true,
    decided: true,
    effective_at: "2026-08-25T11:00:00Z"
  };
  state.privacyDecisions.push({
    decision_id: "12000000-0000-4000-8000-000000000012",
    subject_id: "13000000-0000-4000-8000-000000000013",
    policy_version: 1,
    surface: "public",
    analytics: true,
    marketing: false,
    effective_at: "2026-08-25T11:00:00Z"
  });

  await page.goto("http://127.0.0.1:4174/privacy#consent-history");
  const history = page.getByRole("region", { name: "This browser's consent history" });
  await expect(history).toContainText("Analytics accepted");
  await expect(history).toContainText("Marketing rejected");
  await expect(history).not.toContainText("12000000-0000-4000-8000-000000000012");
  await expect(history).not.toContainText("13000000-0000-4000-8000-000000000013");
  await history.getByRole("button", { name: "Erase this browser's privacy data" }).click();
  await expect(history).toContainText("Your login, Accounts, billing and Affiliate records are not affected");
  await history.getByRole("button", { name: "Confirm public-site data erasure" }).click();

  await expect(history).toContainText("public-site consent receipts and raw analytics were erased");
  await expect(history).toContainText("No saved public-site consent decision exists");
  await expect(page.getByRole("button", { name: "Accept analytics" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reject non-essential" })).toBeVisible();
  await page.getByRole("button", { name: "Manage preferences" }).click();
  await expect(page.getByText("Marketing tracking is not used.", { exact: true })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: /Marketing/ })).toHaveCount(0);
  expect(state.erasures).toBe(1);
  expect(state.analyticsEvents).toEqual([]);
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("@text-zoom public acquisition remains usable at 200% text size", async ({ page }) => {
  const routes = [
    { path: "/", heading: "Know what needs you next." },
    { path: "/pricing", heading: /\$50 a month/ },
    ...publicFeatureAndPolicyRoutes
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(`http://127.0.0.1:4174${route.path}`);
      await page.addStyleTag({ content: "html { font-size: 200% !important; }" });
      await expect.poll(() => page.evaluate(() => Number.parseFloat(getComputedStyle(document.documentElement).fontSize))).toBeGreaterThanOrEqual(32);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
      expect(state.analyticsEvents, `${route.path} emitted before an analytics decision`).toEqual([]);
    });
  }
});

test("@browser-zoom public acquisition reflows at 400% browser scale", async ({ page, context }) => {
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
    { path: "/", heading: "Know what needs you next." },
    { path: "/pricing", heading: /\$50 a month/ },
    ...publicFeatureAndPolicyRoutes
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      await page.goto(`http://127.0.0.1:4174${route.path}`);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
      expect(state.analyticsEvents, `${route.path} emitted before an analytics decision`).toEqual([]);
    });
  }
});
