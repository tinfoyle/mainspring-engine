import AxeBuilder from "@axe-core/playwright";
import { randomUUID } from "node:crypto";
import { expect, test, type APIRequestContext, type BrowserContext, type CDPSession, type Page } from "@playwright/test";

const mailpitURL = process.env.SPYGLASS_OPERATIONS_MAILPIT_URL;
const operationsURL = process.env.SPYGLASS_OPERATIONS_BASE_URL;
const roleWaitMilliseconds = Number(process.env.SPYGLASS_OPERATIONS_ROLE_WAIT_MS ?? "60000");
if (!mailpitURL || !operationsURL) throw new Error("Operations Console browser environment is incomplete");
if (!Number.isFinite(roleWaitMilliseconds) || roleWaitMilliseconds < 5_000 || roleWaitMilliseconds > 300_000) throw new Error("Operations staff role wait is invalid");

interface MailpitSearch { readonly messages: ReadonlyArray<{ readonly ID: string }>; }
interface MailpitMessage { readonly ID: string; readonly Text: string; }

async function waitForMailLink(request: APIRequestContext, email: string): Promise<string> {
  let link = "";
  await expect.poll(async () => {
    const searchResponse = await request.get(`${mailpitURL}/api/v1/search`, { params: { query: `to:${email}` } });
    if (!searchResponse.ok()) return "";
    const search = await searchResponse.json() as MailpitSearch;
    for (const item of search.messages) {
      const response = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(item.ID)}`);
      if (!response.ok()) continue;
      const message = await response.json() as MailpitMessage;
      link = message.Text.split(/\s+/).find((value) => {
        try { return new URL(value).pathname === "/verify"; } catch { return false; }
      }) ?? "";
      if (link) return link;
    }
    return "";
  }, { timeout: 20_000 }).toContain("/verify");
  return link;
}

async function register(page: Page, context: BrowserContext, request: APIRequestContext, purpose: string) {
  const suffix = `${purpose}-${randomUUID()}`;
  const email = `${suffix}@example.com`;
  const password = `Local operations ${suffix}!`;
  await context.clearCookies();
  await page.goto("/signup?offer=team-monthly-v2");
  await page.getByLabel("Your name").fill(purpose === "staff" ? "Local Operations Staff" : "Local Support Customer");
  await page.getByLabel("Work email").fill(email);
  await page.getByLabel("Business name").fill(`${purpose} workshop ${suffix.slice(-12)}`);
  await page.getByRole("button", { name: "Continue" }).click();
  const verification = await waitForMailLink(request, email);
  await page.goto(verification);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Create identity and Account" }).click();
  await expect(page).toHaveURL(/\/login\?.*status=verified/);
  await page.getByLabel("Email address").fill(email);
  await page.locator('input[name="password"]').fill(password);
  await page.locator('form[action="/login"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/app\/your-turn(?:[?#]|$)/);
  return { email, password };
}

async function addVirtualAuthenticator(client: CDPSession): Promise<string> {
  const result = await client.send("WebAuthn.addVirtualAuthenticator", { options: {
    protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true,
    isUserVerified: true, automaticPresenceSimulation: true
  } });
  return result.authenticatorId;
}

async function expectAccessible(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"]).analyze();
  expect(results.violations, results.violations.map((item) => `${item.id}: ${item.help}`).join("\n")).toEqual([]);
}

async function expectNoOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth, body: document.body.scrollWidth }));
  expect(dimensions.document, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.viewport);
  expect(dimensions.body, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.viewport);
}

test("staff passkey, exact support view, analytics, and mobile shell complete locally", async ({ page, context, request }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const client = await context.newCDPSession(page);
  await client.send("WebAuthn.enable");
  const authenticatorID = await addVirtualAuthenticator(client);
  try {
    const staff = await register(page, context, request, "staff");
    await page.goto("/app/security");
    await page.getByLabel("Passkey name").fill("Local Operations passkey");
    await page.getByRole("button", { name: "Add passkey" }).click();
    await expect(page.getByText("Local Operations passkey", { exact: true })).toBeVisible();
    const customer = await register(page, context, request, "customer");

    console.log(`OPERATIONS_FIXTURE_EMAIL=${staff.email}`);
    console.log(`OPERATIONS_CUSTOMER_EMAIL=${customer.email}`);
    console.log("OPERATIONS_FIXTURE_WAITING_FOR_ROLE=1");
    await page.waitForTimeout(roleWaitMilliseconds);

    await context.clearCookies();
    await page.goto(operationsURL);
    await page.getByRole("button", { name: "Sign in with passkey" }).click();
    const loginAlert = page.getByRole("alert");
    await expect.poll(async () => ({
      authenticated: await page.getByRole("heading", { level: 1, name: "What needs attention?" }).isVisible(),
      error: await loginAlert.count() ? await loginAlert.textContent() : null
    }), { timeout: 15_000, message: "Operations passkey login should authenticate or expose its safe error" }).toEqual({ authenticated: true, error: null });
    await expect(page.getByRole("heading", { level: 1, name: "What needs attention?" })).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText("No broad customer directory or fuzzy search.")).toBeVisible();
    await expectAccessible(page);

    await page.getByRole("button", { name: "Customer lookup" }).click();
    await page.getByLabel("Exact value").fill(customer.email);
    await page.getByLabel("Support ticket").fill("SUP-LOCAL-1");
    await page.getByLabel("Reason").fill("Customer requested a local billing access review.");
    const searchButton = page.getByRole("button", { name: "Find exact match" });
    await expect(searchButton).toBeEnabled();
    await expect(searchButton).toHaveAttribute("type", "submit");
    await searchButton.click();
    await expect.poll(async () => ({
      found: await page.getByText(customer.email, { exact: false }).isVisible(),
      error: await page.getByRole("alert").count() ? await page.getByRole("alert").textContent() : null
    }), { timeout: 15_000, message: "Exact customer lookup should return its match or expose its safe error" }).toEqual({ found: true, error: null });
    await page.getByRole("button", { name: "Open 15-minute view" }).click();
    await expect.poll(async () => ({
      opened: await page.getByText("Read-only support view", { exact: true }).isVisible(),
      error: await page.getByRole("alert").count() ? await page.getByRole("alert").textContent() : null
    }), { timeout: 15_000, message: "Support view should open or expose its safe error" }).toEqual({ opened: true, error: null });
    await expect(page.getByText("Read-only support view", { exact: true })).toBeVisible();
    await expect(page.locator("#account-view-title")).toHaveText(/customer workshop/);
    await expect(page.getByRole("heading", { name: "Billing" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Access" })).toBeVisible();
    await expectNoOverflow(page);
    await expectAccessible(page);
    await page.getByRole("button", { name: "Close and revoke" }).click();
    await expect(page.getByText("Read-only support view", { exact: true })).toBeHidden();

    await page.getByRole("button", { name: "Analytics" }).click();
    await page.getByLabel("Review ticket").fill("AN-LOCAL-1");
    await page.getByLabel("Reason").fill("Review aggregate local onboarding behavior.");
    const reportButton = page.getByRole("button", { name: "Run report" });
    await expect(reportButton).toBeEnabled();
    await expect(reportButton).toHaveAttribute("type", "submit");
    await reportButton.click();
    await expect.poll(async () => ({
      loaded: await page.getByText(/aggregate rows/).isVisible(),
      error: await page.getByRole("alert").count() ? await page.getByRole("alert").textContent() : null
    }), { timeout: 15_000, message: "Aggregate analytics report should load or expose its safe error" }).toEqual({ loaded: true, error: null });

    await page.getByRole("button", { name: "Billing issues" }).click();
    await page.getByLabel("Ticket").fill("BILL-LOCAL-1");
    await page.getByLabel("Reason").fill("Review the classified local billing queue.");
    const billingResponse = page.waitForResponse((response) => response.url().includes("/billing/failures/reports") && response.request().method() === "POST");
    await page.getByRole("button", { name: "Inspect failures" }).click();
    expect((await billingResponse).status()).toBe(200);
    await expect.poll(async () => await page.getByText("No classified billing failures are waiting.").isVisible() || await page.locator(".result-card").count() > 0).toBe(true);
    await expectAccessible(page);

    await page.getByRole("button", { name: "Privacy rights" }).click();
    await page.getByLabel("Ticket").fill("PRIV-LOCAL-1");
    await page.getByLabel("Reason").fill("Review the minimized local privacy deadline queue.");
    const privacyResponse = page.waitForResponse((response) => response.url().includes("/privacy-rights/reports/open") && response.request().method() === "POST");
    await page.getByRole("button", { name: "Load requests due within 31 days" }).click();
    expect((await privacyResponse).status()).toBe(200);
    await expect(page.getByRole("heading", { name: "Fulfill a rights request" })).toBeVisible();
    await expectAccessible(page);

    await page.getByRole("button", { name: "Affiliates" }).click();
    await expect(page.getByRole("heading", { name: "Review one Affiliate" })).toBeVisible();
    await expectAccessible(page);

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole("button", { name: "Menu" })).toBeVisible();
    await page.getByRole("button", { name: "Menu" }).click();
    await expect(page.getByRole("navigation", { name: "Operations navigation" })).toBeVisible();
    await expectNoOverflow(page);
    await expectAccessible(page);

    await context.clearCookies();
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/login");
    await page.getByLabel("Email address").fill(customer.email);
    await page.locator('input[name="password"]').fill(customer.password);
    await page.locator('form[action="/login"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/app\/your-turn(?:[?#]|$)/);
    await page.goto("/app/security");
    await expect(page.getByRole("heading", { name: "When Support viewed your details" })).toBeVisible();
    await expect(page.getByText("SUP-LOCAL-1", { exact: false }).first()).toBeVisible();
    await expect(page.getByText("Local Operations Staff", { exact: false }).first()).toBeVisible();
    await expectAccessible(page);
    expect(errors).toEqual([]);
  } finally {
    await client.send("WebAuthn.removeVirtualAuthenticator", { authenticatorId: authenticatorID }).catch(() => undefined);
    await client.send("WebAuthn.disable").catch(() => undefined);
  }
});
