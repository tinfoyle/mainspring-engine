import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

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
    { path: "/signup?offer=team-monthly-v1", heading: "Your first clear view is free.", formHeading: "Create your Account" },
    { path: "/forgot-password", heading: "Restore access. Keep every Account.", formHeading: "Find your identity" },
    { path: "/reset-password?token=local-layout-proof", heading: "Set a new key to the view ahead.", formHeading: "Reset your password" },
    { path: "/verify?token=local-layout-proof&offer=team-monthly-v1", heading: "Secure the view ahead.", formHeading: "Choose your password" },
    { path: "/contact-change/verify?token=local-layout-proof", heading: "Move the signal. Keep the identity.", formHeading: "Verify the new email" }
  ] as const;

  for (const route of routes) {
    await test.step(route.path, () => expectIdentityFrame(page, route.path, route.heading, route.formHeading));
  }
  expect(runtimeErrors).toEqual([]);
});

test("identity consent rejection remains equal, reversible, and non-blocking", async ({ page, context }) => {
  await context.clearCookies();
  await page.goto("/login?return_to=%2Fapp");
  const panel = page.locator("#privacy-consent");
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("button", { name: "Accept analytics" })).toBeVisible();
  const reject = panel.getByRole("button", { name: "Reject non-essential" });
  await expect(reject).toBeVisible();
  await reject.click();
  await expect(panel).toBeHidden();
  const reopen = page.getByRole("button", { name: "Privacy choices" });
  await expect(reopen).toBeVisible();
  await reopen.click();
  await expect(panel).toBeVisible();
  await expect(page.getByRole("checkbox", { name: /Analytics/ })).not.toBeChecked();
  await expect(page.getByRole("checkbox", { name: /Marketing/ })).not.toBeChecked();
  await expect(page.getByLabel("Email address")).toBeEnabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});

test("offer continuity, native validation, and incomplete-link recovery fail safely", async ({ page }) => {
  await page.goto("/signup?offer=team-monthly-v1");
  await expect(page.locator('input[name="offer_code"]')).toHaveValue("team-monthly-v1");
  await page.getByRole("button", { name: "Continue securely" }).click();
  await expect(page.locator("input:invalid")).toHaveCount(3);
  await expect(page).toHaveURL(/\/signup\?offer=team-monthly-v1$/);

  let response = await page.goto("/verify");
  expect(response?.status()).toBe(400);
  await expect(page.getByRole("alert")).toContainText("verification link is incomplete");
  await expectAccessible(page);

  response = await page.goto("/reset-password");
  expect(response?.status()).toBe(400);
  await expect(page.getByRole("alert")).toContainText("recovery link is incomplete");
  await expectAccessible(page);

  response = await page.goto("/contact-change/verify");
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("alert")).toContainText("email verification link is incomplete");
  await expect(page.getByRole("button", { name: "Change identity email" })).toBeDisabled();
  await expectNoHorizontalOverflow(page);
  await expectAccessible(page);
});
