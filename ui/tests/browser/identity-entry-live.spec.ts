import AxeBuilder from "@axe-core/playwright";
import { randomUUID } from "node:crypto";
import { expect, test, type APIRequestContext, type BrowserContext, type CDPSession, type Page } from "@playwright/test";

const mailpitURL = process.env.SPYGLASS_IDENTITY_MAILPIT_URL;
if (!mailpitURL) throw new Error("SPYGLASS_IDENTITY_MAILPIT_URL is required");

interface MailpitSearch {
  readonly total: number;
  readonly messages: ReadonlyArray<{ readonly ID: string }>;
}

interface MailpitMessage {
  readonly ID: string;
  readonly Text: string;
}

async function waitForMailLink(request: APIRequestContext, email: string, path: string): Promise<{ id: string; link: string }> {
  let result = { id: "", link: "" };
  await expect.poll(async () => {
    const searchResponse = await request.get(`${mailpitURL}/api/v1/search`, { params: { query: `to:${email}` } });
    if (!searchResponse.ok()) return "";
    const search = await searchResponse.json() as MailpitSearch;
    for (const item of search.messages) {
      const messageResponse = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(item.ID)}`);
      if (!messageResponse.ok()) continue;
      const message = await messageResponse.json() as MailpitMessage;
      const link = message.Text.split(/\s+/).find((value) => {
        try {
          const parsed = new URL(value);
          return parsed.origin === "https://app.infiniteocean.localhost:8444" && parsed.pathname === path;
        } catch {
          return false;
        }
      });
      if (link) {
        result = { id: message.ID, link };
        return link;
      }
    }
    return "";
  }, { message: `wait for ${path} mail to the synthetic identity`, timeout: 20_000 }).toContain(path);
  return result;
}

async function deleteMail(request: APIRequestContext, id: string): Promise<void> {
  const response = await request.delete(`${mailpitURL}/api/v1/messages`, { data: { IDs: [id] } });
  expect(response.ok()).toBe(true);
}

interface ConnectedIdentity {
  readonly email: string;
  readonly password: string;
  readonly suffix: string;
}

async function registerVerifiedIdentity(
  page: Page,
  context: BrowserContext,
  request: APIRequestContext,
  projectName: string,
  purpose: string
): Promise<ConnectedIdentity> {
  const suffix = `${projectName.replace(/[^a-z0-9]/gi, "-")}-${randomUUID()}`;
  const email = `${purpose}-${suffix}@example.com`;
  const password = `Initial identity ${suffix}!`;

  await context.clearCookies();
  await page.goto("/signup");
  const pageOrigin = await page.evaluate(() => window.location.origin);
  let submittedOrigin = "not observed";
  page.on("request", (browserRequest) => {
    if (browserRequest.method() === "POST" && new URL(browserRequest.url()).pathname === "/signup") {
      submittedOrigin = browserRequest.headers().origin ?? "missing";
    }
  });
  await page.getByLabel("Your name").fill("Connected Identity");
  await page.getByLabel("Work email").fill(email);
  await page.getByLabel("Business name").fill(`Connected ${suffix.slice(0, 44)}`);
  await page.getByRole("button", { name: "Continue securely" }).click();
  expect(submittedOrigin).toBe(pageOrigin);
  await expect(page.locator(".alert"), `page origin ${pageOrigin}; submitted origin ${submittedOrigin}`).toContainText("Your verification link is on its way.");
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);

  const verification = await waitForMailLink(request, email, "/verify");
  await deleteMail(request, verification.id);
  await page.goto(verification.link);
  await expect(page.getByRole("heading", { level: 2, name: "Choose your password" })).toBeVisible();
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Create identity and Account" }).click();
  const verificationDenial = page.locator(".alert.error");
  if (await verificationDenial.count()) throw new Error(`verification denial: ${await verificationDenial.innerText()}`);
  await expect(page).toHaveURL(/\/login\?.*status=verified/);
  await expect(page.locator(".alert")).toContainText("Identity verified. Sign in to open Spyglass.");
  await expectAccessible(page);

  await page.getByLabel("Email address").fill(email);
  await page.locator('input[name="password"]').fill(password);
  await page.locator('form[action="/login"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/app\/your-turn$/);
  await expect(page.getByRole("heading", { level: 1, name: "Your Turn" })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
  return { email, password, suffix };
}

async function addVirtualAuthenticator(client: CDPSession): Promise<string> {
  const result = await client.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2",
      transport: "internal",
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true
    }
  });
  return result.authenticatorId;
}

async function expectAccessible(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22a", "wcag22aa"])
    .analyze();
  expect(results.violations, results.violations.map((violation) =>
    `${violation.id}: ${violation.help}\n${violation.nodes.map((node) => `  ${node.target.join(" ")} — ${node.failureSummary}`).join("\n")}`
  ).join("\n\n")).toEqual([]);
}

async function expectNoHorizontalOverflow(page: Page): Promise<void> {
  const overflow = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
    body: document.body.scrollWidth
  }));
  expect(overflow.document, JSON.stringify(overflow)).toBeLessThanOrEqual(overflow.viewport);
  expect(overflow.body, JSON.stringify(overflow)).toBeLessThanOrEqual(overflow.viewport);
}

async function expectIdentityFrame(page: Page, path: string, heading: string, formHeading: string): Promise<void> {
  const response = await page.goto(path);
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: formHeading })).toBeVisible();
  await page.keyboard.press("Tab");
  await expect(page.locator(".skip-link")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("main#main-content")).toBeFocused();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
}

test("identity entry and recovery pages retain one accessible responsive frame", async ({ page }) => {
  const runtimeErrors: string[] = [];
  page.on("pageerror", (error) => runtimeErrors.push(error.message));

  const routes = [
    { path: "/login?return_to=%2Fapp", heading: "Find the signal. Move the business.", formHeading: "Sign in to Spyglass" },
    { path: "/signup?offer=team-monthly-v2", heading: "Build a clear operating view.", formHeading: "Create your Account" },
    { path: "/forgot-password", heading: "Restore access. Keep every Account.", formHeading: "Find your identity" },
    { path: "/reset-password?token=local-layout-proof", heading: "Set a new key to the view ahead.", formHeading: "Reset your password" },
    { path: "/verify?token=local-layout-proof&offer=team-monthly-v2", heading: "Secure the view ahead.", formHeading: "Choose your password" },
    { path: "/contact-change/verify?token=local-layout-proof", heading: "Move the signal. Keep the identity.", formHeading: "Verify the new email" }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, () => expectIdentityFrame(page, route.path, route.heading, route.formHeading));
  }
  expect(runtimeErrors).toEqual([]);
});

test("@text-zoom identity entry and recovery pages reflow at 200% text size", async ({ page }) => {
  const runtimeErrors: string[] = [];
  page.on("pageerror", (error) => runtimeErrors.push(error.message));
  const routes = [
    { path: "/login?return_to=%2Fapp", heading: "Find the signal. Move the business." },
    { path: "/signup?offer=team-monthly-v2", heading: "Build a clear operating view." },
    { path: "/forgot-password", heading: "Restore access. Keep every Account." },
    { path: "/reset-password?token=local-layout-proof", heading: "Set a new key to the view ahead." },
    { path: "/verify?token=local-layout-proof&offer=team-monthly-v2", heading: "Secure the view ahead." },
    { path: "/contact-change/verify?token=local-layout-proof", heading: "Move the signal. Keep the identity." }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, async () => {
      const response = await page.goto(route.path);
      expect(response?.status()).toBe(200);
      await page.addStyleTag({ content: "html { font-size: 200% !important; }" });
      await expect.poll(() => page.evaluate(() => Number.parseFloat(getComputedStyle(document.documentElement).fontSize))).toBeGreaterThanOrEqual(32);
      await expect(page.getByRole("heading", { level: 1, name: route.heading })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    });
  }
  expect(runtimeErrors).toEqual([]);
});

test("identity consent rejection remains equal, compact, reversible, and non-blocking", async ({ page, context }, testInfo) => {
  await context.clearCookies();
  await page.goto("/login?return_to=%2Fapp");
  const panel = page.locator("#privacy-consent");
  await expect(panel).toBeVisible();
  const accept = panel.getByRole("button", { name: "Accept analytics" });
  await expect(accept).toBeVisible();
  const reject = panel.getByRole("button", { name: "Reject non-essential" });
  await expect(reject).toBeVisible();
  const choiceStyles = await Promise.all([accept, reject].map((choice) => choice.evaluate((element) => {
    const style = getComputedStyle(element);
    return { backgroundColor: style.backgroundColor, border: style.border, color: style.color };
  })));
  expect(choiceStyles[0]).toEqual(choiceStyles[1]);
  if (testInfo.project.name === "chromium-phone") {
    const layout = await page.evaluate(() => {
      const consent = document.querySelector<HTMLElement>("#privacy-consent")?.getBoundingClientRect();
      const formHeading = document.querySelector<HTMLElement>(".auth-panel h2")?.getBoundingClientRect();
      return {
        consentHeight: consent?.height ?? Number.POSITIVE_INFINITY,
        formHeadingTop: formHeading?.top ?? Number.POSITIVE_INFINITY,
        viewportHeight: innerHeight
      };
    });
    expect(layout.consentHeight).toBeLessThanOrEqual(300);
    expect(layout.formHeadingTop).toBeLessThan(layout.viewportHeight);
  }
  await reject.click();
  await expect(panel).toBeHidden();
  const reopen = page.getByRole("button", { name: "Privacy choices" });
  await expect(reopen).toBeVisible();
  await reopen.click();
  await expect(panel).toBeVisible();
  await expect(page.getByRole("checkbox", { name: /Analytics/ })).not.toBeChecked();
  await expect(page.getByRole("checkbox", { name: /Marketing/ })).toHaveCount(0);
  await expect(panel.getByText("Spyglass does not ask you to consent to one")).toBeVisible();
  await expect(page.getByLabel("Email address")).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("offer continuity, native validation, and incomplete-link recovery fail safely", async ({ page }) => {
  await page.goto("/signup?offer=team-monthly-v2");
  await expect(page.locator('input[name="offer_code"]')).toHaveValue("team-monthly-v2");
  await page.getByRole("button", { name: "Continue securely" }).click();
  await expect(page.locator("input:invalid")).toHaveCount(3);
  await expect(page).toHaveURL(/\/signup\?offer=team-monthly-v2$/);

  let response = await page.goto("/verify");
  expect(response?.status()).toBe(400);
  await expect(page.locator(".alert.error")).toContainText("verification link is incomplete");
  await expectAccessible(page);

  response = await page.goto("/reset-password");
  expect(response?.status()).toBe(400);
  await expect(page.locator(".alert.error")).toContainText("recovery link is incomplete");
  await expectAccessible(page);

  response = await page.goto("/contact-change/verify");
  expect(response?.status()).toBe(200);
  await expect(page.locator(".alert.error")).toContainText("email verification link is incomplete");
  await expect(page.getByRole("button", { name: "Change identity email" })).toBeDisabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test.describe("connected identity success", () => {
  test("registration, email verification, password login, and recovery complete against local services", async ({ page, context, request }, testInfo) => {
    test.setTimeout(120_000);
    const { email, password: initialPassword, suffix } = await registerVerifiedIdentity(page, context, request, testInfo.project.name, "identity");
    const replacementPassword = `Recovered identity ${suffix}!`;

    await context.clearCookies();
    await page.goto("/forgot-password?return_to=%2Fapp%2Fyour-turn");
    await page.getByLabel("Email address").fill(email);
    await page.getByRole("button", { name: "Send recovery link" }).click();
    await expect(page.locator(".alert")).toContainText("If that email belongs to an Infinite Ocean identity, a recovery link is on its way.");

    const recovery = await waitForMailLink(request, email, "/reset-password");
    await deleteMail(request, recovery.id);
    await page.goto(recovery.link);
    await expect(page.getByRole("heading", { level: 2, name: "Reset your password" })).toBeVisible();
    await page.getByLabel("New password").fill(replacementPassword);
    await page.getByRole("button", { name: "Update password" }).click();
    await expect(page).toHaveURL(/\/login\?.*status=password_reset/);
    await expect(page.locator(".alert")).toContainText("Password updated. Sign in again on every device.");

    await page.getByLabel("Email address").fill(email);
    await page.locator('input[name="password"]').fill(initialPassword);
    await page.locator('form[action="/login"] button[type="submit"]').click();
    await expect(page.locator(".alert.error")).toContainText("The email or password is incorrect.");
    await page.locator('input[name="password"]').fill(replacementPassword);
    await page.locator('form[action="/login"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/app\/your-turn$/);
    await expect(page.getByRole("heading", { level: 1, name: "Your Turn" })).toBeVisible();
    await expectNoHorizontalOverflow(page);
    await expectAccessible(page);
  });

  test("@virtual-passkey owner enrollment, discoverable login, and lost-authenticator replacement complete locally", async ({ page, context, request }, testInfo) => {
    test.setTimeout(180_000);
    const client = await context.newCDPSession(page);
    await client.send("WebAuthn.enable");
    let authenticatorID = await addVirtualAuthenticator(client);
    try {
      const { email, password } = await registerVerifiedIdentity(page, context, request, testInfo.project.name, "passkey");
      const firstPasskey = "Local virtual passkey";
      const replacementPasskey = "Recovered virtual passkey";

      await page.goto("/app/security");
      await expect(page.getByRole("heading", { level: 1, name: "Security follows you" })).toBeVisible();
      await expect(page.getByRole("heading", { level: 2, name: "Setup incomplete" })).toBeVisible();
      await page.getByLabel("Passkey name").fill(firstPasskey);
      await page.getByRole("button", { name: "Add passkey" }).click();
      await expect(page.getByText(firstPasskey, { exact: true })).toBeVisible();
      await expect(page.getByRole("heading", { level: 2, name: "Setup incomplete" })).toBeVisible();

      await page.getByRole("button", { name: "Create recovery codes" }).click();
      const codes = page.locator(".recovery-code-panel code");
      await expect(codes).toHaveCount(10);
      const recoveryCode = (await codes.first().innerText()).trim();
      expect(recoveryCode).toMatch(/^[0-9a-f]{4}(?:-[0-9a-f]{4}){7}$/);
      await expect(page.getByRole("heading", { level: 2, name: "Identity secured" })).toBeVisible();
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);

      await page.getByRole("button", { name: "Confirm with a passkey" }).click();
      await expect(page.locator('[aria-live="polite"]')).toContainText("Passkey confirmed.");
      await page.waitForLoadState("networkidle");

      await context.clearCookies();
      await page.goto("/login");
      await page.getByRole("button", { name: "Sign in with a passkey" }).click();
      await expect.poll(async () => {
        if (new URL(page.url()).pathname !== "/login") return "navigated";
        const status = await page.locator("#passkey-status").innerText();
        return status && status !== "Waiting for your passkey…" ? status : "pending";
      }, { timeout: 10_000 }).not.toBe("pending");
      if (new URL(page.url()).pathname === "/login") {
        throw new Error(`initial passkey login failed: ${await page.locator("#passkey-status").innerText()}`);
      }
      await expect(page).toHaveURL(/\/app\/your-turn$/);
      await expect(page.getByRole("heading", { level: 1, name: "Your Turn" })).toBeVisible();
      await page.waitForLoadState("networkidle");

      await client.send("WebAuthn.removeVirtualAuthenticator", { authenticatorId: authenticatorID });
      authenticatorID = await addVirtualAuthenticator(client);
      await context.clearCookies();
      await page.goto("/login");
      await page.getByLabel("Email address").fill(email);
      await page.locator('input[name="password"]').fill(password);
      await page.locator('form[action="/login"] button[type="submit"]').click();
      await expect(page).toHaveURL(/\/app\/your-turn$/);

      await page.goto("/app/security");
      await page.getByLabel("Current password").fill(password);
      await page.getByRole("button", { name: "Confirm password" }).click();
      await expect(page.locator('[aria-live="polite"]')).toContainText("Password confirmed");
      await page.locator("summary").filter({ hasText: "Lost every passkey?" }).click();
      await page.getByLabel("Saved recovery code").fill(recoveryCode);
      await page.getByRole("button", { name: "Use recovery code" }).click();
      await expect(page.locator('[aria-live="polite"]')).toContainText("Recovery code accepted");
      await page.getByLabel("Passkey name").fill(replacementPasskey);
      await page.getByRole("button", { name: "Add passkey" }).click();
      await expect(page.getByText(replacementPasskey, { exact: true })).toBeVisible();
      await expect(page.getByText("9 remaining", { exact: true })).toBeVisible();
      await page.waitForLoadState("networkidle");
      const replacementAuthenticator = await client.send("WebAuthn.getCredentials", { authenticatorId: authenticatorID });
      expect(replacementAuthenticator.credentials).toHaveLength(1);
      await page.reload();
      await expect(page.getByRole("heading", { level: 1, name: "Security follows you" })).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.getByRole("button", { name: "Confirm with a passkey" }).click();
      await expect(page.locator('[aria-live="polite"]')).toContainText("Passkey confirmed.");
      await expectNoHorizontalOverflow(page);
      await expectAccessible(page);
    } finally {
      await client.send("WebAuthn.removeVirtualAuthenticator", { authenticatorId: authenticatorID }).catch(() => undefined);
      await client.send("WebAuthn.disable").catch(() => undefined);
    }
  });
});
