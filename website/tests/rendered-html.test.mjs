import assert from "node:assert/strict";
import test from "node:test";

async function render(path = "/") {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}-${path}`);
  const { default: worker } = await import(workerUrl.href);
  return worker.fetch(new Request(`http://localhost${path}`, { headers: { accept: "text/html" } }), { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } }, { waitUntil() {}, passThroughOnException() {} });
}

test("renders the Infinite Ocean Spyglass home page", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  const html = await response.text();
  assert.match(html, /<title>Infinite Ocean: Spyglass<\/title>/i);
  assert.match(html, /See the whole business/);
  assert.match(html, /Move what matters/);
  assert.match(html, /Create a free account/);
  assert.match(html, /Work/);
  assert.match(html, /Agents/);
  assert.match(html, /Finance/);
  assert.match(html, /Marketing/);
  assert.doesNotMatch(html, /codex-preview|react-loading-skeleton|Mainspring/i);
});

test("renders the public package and signup journeys", async () => {
  const [packages, signup, pricing] = await Promise.all([render("/packages"), render("/signup"), render("/pricing")]);
  assert.equal(packages.status, 200);
  assert.equal(signup.status, 200);
  assert.equal(pricing.status, 200);
  assert.match(await packages.text(), /Feature packages/);
  const signupHtml = await signup.text();
  assert.match(signupHtml, /No payment information required/);
  assert.match(signupHtml, /app\.infiniteocean\.net\/signup/);
  assert.match(await pricing.text(), /No card required/);
});
