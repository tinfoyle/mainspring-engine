import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import axe from "axe-core";
import { JSDOM } from "jsdom";

async function reservePort() {
  const listener = createServer();
  await new Promise((resolve, reject) => {
    listener.once("error", reject);
    listener.listen(0, "127.0.0.1", resolve);
  });
  const address = listener.address();
  assert.ok(address && typeof address === "object", "could not reserve a public verification port");
  await new Promise((resolve, reject) => listener.close((error) => error ? reject(error) : resolve()));
  return String(address.port);
}

const port = await reservePort();
const origin = `http://127.0.0.1:${port}`;
const routes = [
  "/",
  "/features",
  "/features/your-turn",
  "/features/work",
  "/features/knowledge",
  "/features/baseline",
  "/features/agents",
  "/features/schedules",
  "/features/finance",
  "/features/marketing",
  "/features/integrations",
  "/features/account-administration",
  "/features/security",
  "/features/export-lifecycle",
  "/pricing",
  "/privacy",
  "/affiliate-terms"
];

const serverOutput = [];
const server = spawn(process.execPath, ["apps/public/.output/server/index.mjs"], {
  env: { ...process.env, HOST: "127.0.0.1", NITRO_HOST: "127.0.0.1", NITRO_PORT: port },
  stdio: ["ignore", "pipe", "pipe"]
});
server.stdout.on("data", (chunk) => serverOutput.push(chunk.toString()));
server.stderr.on("data", (chunk) => serverOutput.push(chunk.toString()));

function formatViolations(violations) {
  return violations.map((violation) => {
    const nodes = violation.nodes.map((node) => `    ${node.target.join(" ")} — ${node.failureSummary}`).join("\n");
    return `${violation.id}: ${violation.help}\n${nodes}`;
  }).join("\n\n");
}

async function waitUntilReady() {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (server.exitCode !== null) break;
    try {
      const response = await fetch(origin, { headers: { accept: "text/html" } });
      if (response.ok) return;
    } catch {
      // The production server is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`public production server did not become ready\n${serverOutput.join("")}`);
}

try {
  await waitUntilReady();
  for (const route of routes) {
    const response = await fetch(`${origin}${route}`, { headers: { accept: "text/html" } });
    assert.equal(response.status, 200, `${route} did not render`);
    const dom = new JSDOM(await response.text(), { url: `${origin}${route}`, runScripts: "outside-only" });
    const { document } = dom.window;

    assert.equal(document.documentElement.lang, "en", `${route} must declare English`);
    assert.ok(document.title.trim(), `${route} must have a title`);
    assert.equal(document.querySelectorAll("header.site-header").length, 1, `${route} must have one site header`);
    assert.equal(document.querySelectorAll("main").length, 1, `${route} must have one main landmark`);
    assert.equal(document.querySelectorAll("footer.site-footer").length, 1, `${route} must have one site footer`);
    assert.equal(document.querySelectorAll("h1").length, 1, `${route} must have one primary heading`);

    const main = document.querySelector("main");
    assert.equal(main?.id, "main", `${route} main must be the skip-link target`);
    assert.equal(main?.getAttribute("tabindex"), "-1", `${route} main must accept programmatic focus`);
    const firstLink = document.querySelector("body a");
    assert.equal(firstLink?.classList.contains("skip-link"), true, `${route} must begin with the skip link`);
    assert.equal(firstLink?.getAttribute("href"), "#main", `${route} skip link must target main`);

    const ids = [...document.querySelectorAll("[id]")].map((element) => element.id);
    assert.equal(new Set(ids).size, ids.length, `${route} element IDs must be unique`);

    dom.window.eval(axe.source);
    const results = await dom.window.axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
      rules: {
        // jsdom cannot calculate layout or rendered pixel colors. Those remain
        // part of the real-browser and assistive-technology release matrix.
        "color-contrast": { enabled: false }
      }
    });
    assert.equal(results.violations.length, 0, `Automated accessibility violations on ${route}:\n${formatViolations(results.violations)}`);
    dom.window.close();
  }
  console.log(`public accessibility verification passed for ${routes.length} production routes`);
} finally {
  const exited = new Promise((resolve) => {
    if (server.exitCode !== null) resolve();
    else server.once("exit", resolve);
  });
  server.kill("SIGTERM");
  await Promise.race([
    exited,
    new Promise((resolve) => setTimeout(resolve, 2000))
  ]);
  if (server.exitCode === null) server.kill("SIGKILL");
}
