import { defineConfig, devices } from "@playwright/test";

const accountBaseURL = process.env.SPYGLASS_OPERATIONS_ACCOUNT_BASE_URL;
if (!accountBaseURL) throw new Error("SPYGLASS_OPERATIONS_ACCOUNT_BASE_URL is required");

export default defineConfig({
  testDir: "./tests/browser",
  testMatch: "operations-console-live.spec.ts",
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: "list",
  webServer: {
    command: "node tests/browser/local-edge-port-forward.mjs",
    url: "http://127.0.0.1:4176/health/ready",
    reuseExistingServer: false,
    timeout: 30_000
  },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: accountBaseURL,
    viewport: { width: 1280, height: 900 },
    ignoreHTTPSErrors: true,
    locale: "en-US",
    timezoneId: "America/New_York",
    trace: "off"
  }
});
