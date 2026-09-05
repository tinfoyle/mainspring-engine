import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("administrator browses paginated users and teams and opens exact lookup", async ({ page }, testInfo) => {
 const now = new Date().toISOString();
 const calls: Array<{ kind: string; page: number; page_size: number }> = [];
 const lookups: Array<{ kind: string; value: string; ticket: string }> = [];
 await page.route("**/api/operations/v1/**", async route => {
  const path = new URL(route.request().url()).pathname;
  if (path.endsWith("/session")) {
   await route.fulfill({ json: { authentication_method: "google_totp", expires_at: now, staff: { user_id: "10000000-0000-4000-8000-000000000001", display_name: "Test Administrator", state: "active", roles: ["operations_administrator"] } } }); return;
  }
  if (path.endsWith("/lookups")) { lookups.push(route.request().postDataJSON()); await route.fulfill({ json: { results: [] } }); return; }
  const input = route.request().postDataJSON() as { kind: string; page: number; page_size: number };
  calls.push(input);
  const total = input.kind === "users" ? 52 : 2;
  const start = (input.page - 1) * input.page_size;
  const rows = Array.from({ length: Math.max(0, Math.min(input.page_size, total - start)) }, (_, index) => {
   const n = start + index + 1;
   const common = { id: `10000000-0000-4000-8000-${String(n).padStart(12, "0")}`, display_name: `${input.kind === "users" ? "Person" : "Team"} ${n}`, state: "active", created_at: now };
   return input.kind === "users" ? { ...common, email: `person${n}@example.com`, email_verified: n % 2 === 0, team_count: n % 3 } : { ...common, slug: `team-${n}`, account_type: "free", member_count: n };
  });
  await route.fulfill({ json: { ...input, total, users: input.kind === "users" ? rows : [], teams: input.kind === "teams" ? rows : [] } });
 });
 await page.goto("/#directory");
 await expect(page.getByRole("heading", { level: 1, name: "Users & teams" })).toBeVisible();
 await expect(page.locator("caption")).toHaveText("Users 1–25 of 52");
 await expect(page.getByRole("button", { name: "Previous", exact: true })).toBeDisabled();
 await page.getByRole("button", { name: "Next", exact: true }).click();
 await expect(page.locator("caption")).toHaveText("Users 26–50 of 52");
 await page.getByRole("button", { name: "Next", exact: true }).click();
 await expect(page.locator("caption")).toHaveText("Users 51–52 of 52");
 await expect(page.getByRole("button", { name: "Next", exact: true })).toBeDisabled();
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
 expect((await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze()).violations).toEqual([]);
 await page.screenshot({ path: testInfo.outputPath("admin-users.png"), fullPage: true });
 await page.getByRole("button", { name: "Previous", exact: true }).click();
 await expect(page.locator("caption")).toHaveText("Users 26–50 of 52");
 await page.getByLabel("Rows per page").selectOption("50");
 await expect(page.locator("caption")).toHaveText("Users 1–50 of 52");
 await page.getByRole("button", { name: "Teams", exact: true }).click();
 await expect(page.locator("caption")).toHaveText("Teams 1–2 of 2");
 expect(calls.at(-1)).toMatchObject({ kind: "teams", page: 1, page_size: 50 });
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
 const accessibility = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
 expect(accessibility.violations).toEqual([]);
 await page.screenshot({ path: testInfo.outputPath("admin-teams.png"), fullPage: true });
 await page.getByRole("button", { name: "View team Team 1", exact: true }).click();
 await expect(page.getByRole("heading", { level: 1, name: "Find one customer" })).toBeVisible();
 expect(lookups.at(-1)).toMatchObject({ kind: "account_id", value: "10000000-0000-4000-8000-000000000001", ticket: "DIRECTORY-REVIEW" });
 await expect(page.getByText("No team membership found", { exact: false })).toBeVisible();
});
