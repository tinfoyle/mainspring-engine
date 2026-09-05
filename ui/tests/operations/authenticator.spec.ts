import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const session = { authentication_method: "google_totp", expires_at: "2026-10-01T00:00:00Z", staff: { user_id: "10000000-0000-4000-8000-000000000001", display_name: "Test Administrator", state: "active", roles: ["operations_administrator"] } };

test("authenticator setup, recovery-code acknowledgement and admin access", async ({ page }, testInfo) => {
  let signedIn = false;
  await page.route("**/api/operations/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/session")) return route.fulfill(signedIn ? { json: session } : { status: 401, contentType: "application/problem+json", json: { detail: "Sign in required" } });
    if (path.endsWith("/auth")) return route.fulfill({ json: { stage: "enroll", display_name: "Test Administrator" } });
    if (path.endsWith("/enrollment")) return route.fulfill({ json: { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/Spyglass%20Admin:Test?secret=JBSWY3DPEHPK3PXP&issuer=Spyglass%20Admin" } });
    if (path.endsWith("/verify")) {
      expect(route.request().postDataJSON()).toEqual({ code: "123456", recovery: false });
      signedIn = true;
      return route.fulfill({ json: { session, recovery_codes: ["ABCDEF-GHIJKL-MNOPQR-STUVWXYZ", "BCDEFG-HIJKLM-NOPQRS-TUVWXYZA"] } });
    }
    return route.fulfill({ status: 404 });
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Set up your authenticator" })).toBeVisible();
  await expect(page.getByAltText("Scan this QR code with your authenticator app")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect((await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze()).violations).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("authenticator-setup.png"), fullPage: true });
  await page.getByLabel("Authenticator code", { exact: true }).fill("123456");
  await page.getByRole("button", { name: "Confirm authenticator", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open admin", exact: true })).toBeDisabled();
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download recovery codes" }).click();
  expect((await download).suggestedFilename()).toBe("spyglass-admin-recovery-codes.txt");
  await page.getByLabel("I saved my recovery codes.").check();
  await page.getByRole("button", { name: "Open admin", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Admin overview" })).toBeVisible();
  await expect(page.getByText("ABCDEF-GHIJKL-MNOPQR-STUVWXYZ")).toHaveCount(0);
});

test("a recovery code requires replacement enrollment and a bad code stays blocked", async ({ page }) => {
  await page.route("**/api/operations/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/session")) return route.fulfill({ status: 401, contentType: "application/problem+json", json: { detail: "Sign in required" } });
    if (path.endsWith("/auth")) return route.fulfill({ json: { stage: "code", display_name: "Test Administrator" } });
    if (path.endsWith("/verify")) {
      const input = route.request().postDataJSON();
      if (input.recovery) return route.fulfill({ json: { stage: "enroll" } });
      return route.fulfill({ status: 401, contentType: "application/problem+json", json: { detail: "Check your code and try again." } });
    }
    if (path.endsWith("/enrollment")) return route.fulfill({ json: { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/Test?secret=JBSWY3DPEHPK3PXP" } });
    return route.fulfill({ status: 404 });
  });
  await page.goto("/");
  await page.getByLabel("Authenticator code", { exact: true }).fill("000000");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Check your code");
  await expect(page.getByRole("heading", { name: "Admin overview" })).toHaveCount(0);
  await page.getByRole("button", { name: "Lost your authenticator?" }).click();
  await page.getByLabel("Recovery code", { exact: true }).fill("ABCDEF-GHIJKL-MNOPQR-STUVWXYZ");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Set up your authenticator" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Admin overview" })).toHaveCount(0);
});


test("native Google handoff preserves source origin without callback query", async ({ page }) => {
  const origin = "http://127.0.0.1:4178";
  let receivedOrigin = "";
  let receivedReferrer = "";
  await page.route("http://localhost:4178/admin-handoff-result", async route => {
    receivedOrigin = route.request().headers()["origin"] ?? "";
    receivedReferrer = route.request().headers()["referer"] ?? "";
    await route.fulfill({ contentType: "text/html", body: "<h1>Authenticator required</h1>" });
  });
  await page.route(origin + "/handoff-fixture?code=synthetic", route => route.fulfill({
    contentType: "text/html", headers: { "Referrer-Policy": "origin" },
    body: '<form method="post" action="http://localhost:4178/admin-handoff-result"><button>Continue to admin</button></form>'
  }));
  await page.goto(origin + "/handoff-fixture?code=synthetic");
  await page.getByRole("button", { name: "Continue to admin" }).click();
  await expect(page.getByRole("heading", { name: "Authenticator required" })).toBeVisible();
  expect(receivedOrigin).toBe(origin);
  expect(receivedReferrer).toBe(origin + "/");
});
