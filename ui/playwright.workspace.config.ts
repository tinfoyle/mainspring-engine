import { defineConfig, devices } from "@playwright/test";
export default defineConfig({
  testDir: "./tests/browser", testMatch: "workspace.spec.ts", fullyParallel: false, workers: 1,
  retries: 0, reporter: "list",
  use: { baseURL: "http://127.0.0.1:4173", locale: "en-US", timezoneId: "America/New_York", trace: "retain-on-failure" },
  webServer: { command: "npm exec --workspace=@spyglass/app -- vite preview --host 127.0.0.1 --port 4173",
    url: "http://127.0.0.1:4173", reuseExistingServer: false, timeout: 60000 },
  projects: [
    { name: "workspace-chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } } },
    { name: "workspace-firefox", use: { ...devices["Desktop Firefox"], viewport: { width: 1280, height: 900 } } },
    { name: "workspace-phone", use: { ...devices["Pixel 7"], viewport: { width: 360, height: 800 } } },
    { name: "workspace-reflow", use: { ...devices["Desktop Chrome"], viewport: { width: 320, height: 800 } } }
  ]
});
