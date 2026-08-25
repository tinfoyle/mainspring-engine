import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
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
  webServer: {
    command: "npm exec --workspace=@spyglass/app -- vite preview --host 127.0.0.1 --port 4173",
    url: "http://127.0.0.1:4173/health/ready",
    reuseExistingServer: false,
    timeout: 30_000
  },
  projects: [
    {
      name: "chromium-desktop",
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } }
    },
    {
      name: "chromium-phone",
      use: { ...devices["Pixel 7"], viewport: { width: 390, height: 844 } }
    }
  ]
});
