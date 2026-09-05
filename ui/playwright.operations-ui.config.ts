import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/operations", fullyParallel: false, workers: 1, retries: 0,
  reporter: "list", forbidOnly: true,
  use: { baseURL: "http://127.0.0.1:4178", locale: "en-US", trace: "retain-on-failure" },
  webServer: { command: "npm exec --workspace=@spyglass/operations -- vite preview --host 127.0.0.1 --port 4178", url: "http://127.0.0.1:4178", reuseExistingServer: false },
  projects: [
    { name: "firefox", use: { browserName: "firefox", viewport: { width: 1280, height: 900 } } },
    { name: "desktop", use: { browserName: "chromium", viewport: { width: 1280, height: 900 } } },
    { name: "phone", use: { browserName: "chromium", viewport: { width: 360, height: 800 } } }
  ]
});
