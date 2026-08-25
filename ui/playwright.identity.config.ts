import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.SPYGLASS_IDENTITY_BASE_URL;
if (!baseURL) throw new Error("SPYGLASS_IDENTITY_BASE_URL is required");

export default defineConfig({
  testDir: "./tests/browser",
  testMatch: "identity-entry-live.spec.ts",
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
    baseURL,
    ignoreHTTPSErrors: true,
    locale: "en-US",
    timezoneId: "America/New_York",
    trace: "retain-on-failure"
  },
  projects: [
    {
      name: "chromium-desktop",
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "firefox-desktop",
      use: { ...devices["Desktop Firefox"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "webkit-desktop",
      use: { ...devices["Desktop Safari"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "chromium-phone",
      use: { ...devices["Pixel 7"], viewport: { width: 390, height: 844 } }
    }
  ]
});
