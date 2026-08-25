import { defineConfig, devices } from "@playwright/test";

const textZoomChecks = /@text-zoom/;
const browserZoomChecks = /@browser-zoom/;
const standardChecks = /@text-zoom|@browser-zoom/;

export default defineConfig({
  testDir: "./tests/browser",
  testIgnore: "identity-entry-live.spec.ts",
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: "list",
  use: {
    baseURL: "http://127.0.0.1:4173",
    locale: "en-US",
    timezoneId: "America/New_York",
    trace: "retain-on-failure"
  },
  webServer: [
    {
      command: "node tests/browser/public-catalog-fixture.mjs",
      url: "http://127.0.0.1:4175/health/ready",
      reuseExistingServer: false,
      timeout: 30_000
    },
    {
      command: "npm exec --workspace=@spyglass/app -- vite preview --host 127.0.0.1 --port 4173",
      url: "http://127.0.0.1:4173/health/ready",
      reuseExistingServer: false,
      timeout: 30_000
    },
    {
      command: "node apps/public/.output/server/index.mjs",
      url: "http://127.0.0.1:4174/",
      reuseExistingServer: false,
      timeout: 30_000,
      env: {
        HOST: "127.0.0.1",
        NITRO_HOST: "127.0.0.1",
        NITRO_PORT: "4174",
        NUXT_ACCOUNT_API_ORIGIN: "http://127.0.0.1:4175",
        NUXT_PUBLIC_APP_ORIGIN: "http://127.0.0.1:4173",
        SPYGLASS_ACCOUNT_API_ORIGIN: "http://127.0.0.1:4175"
      }
    }
  ],
  projects: [
    {
      name: "chromium-desktop",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "firefox-desktop",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Firefox"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "webkit-desktop",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Safari"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "chromium-phone-360",
      grepInvert: standardChecks,
      use: { ...devices["Pixel 7"], viewport: { width: 360, height: 800 } }
    },
    {
      name: "chromium-phone",
      grepInvert: standardChecks,
      use: { ...devices["Pixel 7"], viewport: { width: 390, height: 844 } }
    },
    {
      name: "chromium-phone-412",
      grepInvert: standardChecks,
      use: { ...devices["Pixel 7"], viewport: { width: 412, height: 915 } }
    },
    {
      name: "chromium-reflow",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 320, height: 800 } }
    },
    {
      name: "chromium-tablet",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 768, height: 1024 }, hasTouch: true }
    },
    {
      name: "chromium-forced-colors",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, forcedColors: "active" }
    },
    {
      name: "chromium-reduced-motion",
      grepInvert: standardChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, contextOptions: { reducedMotion: "reduce" } }
    },
    {
      name: "chromium-text-zoom",
      grep: textZoomChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "chromium-browser-zoom-400",
      grep: browserZoomChecks,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    }
  ]
});
