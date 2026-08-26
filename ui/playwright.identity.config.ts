import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.SPYGLASS_IDENTITY_BASE_URL;
if (!baseURL) throw new Error("SPYGLASS_IDENTITY_BASE_URL is required");
const textZoomChecks = /@text-zoom/;
const virtualPasskeyChecks = /@virtual-passkey/;
const specializedChecks = /@text-zoom|@virtual-passkey/;

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
    // Verification and recovery URLs contain short-lived credentials. Never retain them in browser traces.
    trace: "off"
  },
  projects: [
    {
      name: "chromium-desktop",
      grepInvert: specializedChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "firefox-desktop",
      grepInvert: specializedChecks,
      use: { ...devices["Desktop Firefox"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "webkit-desktop",
      grepInvert: specializedChecks,
      use: { ...devices["Desktop Safari"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "chromium-phone",
      grepInvert: specializedChecks,
      use: { ...devices["Pixel 7"], viewport: { width: 390, height: 844 } }
    },
    {
      name: "chromium-text-zoom",
      grep: textZoomChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, bypassCSP: true }
    },
    {
      name: "chromium-virtual-passkey",
      grep: virtualPasskeyChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    }
  ]
});
