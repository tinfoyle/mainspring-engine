import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("administrator can read traffic, IPs, logs and consented analytics without overflow", async ({ page }, testInfo) => {
  const now = new Date().toISOString();
  await page.route("**/api/operations/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    const body = path.endsWith("/session") ? { authentication_method: "passkey", expires_at: now, staff: { user_id: "10000000-0000-4000-8000-000000000001", display_name: "Test Administrator", state: "active", roles: ["operations_administrator"] } } : path.endsWith("/traffic/reports") ? {
      from: now, to: now, generated_at: now, available_from: now, available_to: now, requests: 12, unique_ips: 1, server_errors: 1, files_read: 4, invalid_records: 0, truncated: false, ip_list_truncated: false,
      ips: [{ ip: "2001:db8:aaaa:bbbb:cccc:dddd:eeee:ffff", requests: 12, first_seen: now, last_seen: now }],
      hosts: [{ label: "app.stage.infiniteocean.net", requests: 12 }], statuses: [{ label: "200", requests: 11 }, { label: "503", requests: 1 }], days: [{ label: now.slice(0, 10), requests: 12 }],
      logs: [{ time: now, ip: "2001:db8:aaaa:bbbb:cccc:dddd:eeee:ffff", host: "app.stage.infiniteocean.net", method: "GET", status: 503, duration_ms: 41 }]
    } : { from: now, to: now, bucket: "day", dimension: "none", minimum_cohort: 5, rows: [] };
    await route.fulfill({ json: body });
  });
  await page.goto("/#traffic");
  await expect(page.getByRole("heading", { level: 1, name: "Traffic & logs" })).toBeVisible();
  await page.getByRole("button", { name: "Load traffic", exact: true }).click();
  await expect(page.locator(".metric-grid")).toContainText("Requests12");
  for (const view of ["Summary", "Unique IPs", "Request logs"]) {
    await page.getByRole("button", { name: view, exact: true }).click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const accessibility = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
    expect(accessibility.violations).toEqual([]);
    if (view === "Summary") await page.screenshot({ path: testInfo.outputPath("traffic-summary.png"), fullPage: true });
  }
  if (await page.getByRole("button", { name: "Menu", exact: true }).isVisible()) await page.getByRole("button", { name: "Menu", exact: true }).click();
  await page.getByRole("button", { name: "Analytics", exact: true }).click();
  await page.getByRole("button", { name: "Run report", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("not a count of zero visitors");
  if (await page.getByRole("button", { name: "Menu", exact: true }).isVisible()) await page.getByRole("button", { name: "Menu", exact: true }).click();
  await page.getByRole("button", { name: "How to use this console", exact: true }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
