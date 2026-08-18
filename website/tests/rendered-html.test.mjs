import assert from "node:assert/strict";
import test from "node:test";
import { publicCatalogFixture } from "./fixtures/public-catalog.mjs";

async function render(path = "/") {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}-${path}`);
  const { default: worker } = await import(workerUrl.href);
  return worker.fetch(new Request(`https://infiniteocean.net${path}`, { headers: { accept: "text/html" } }), { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } }, { waitUntil() {}, passThroughOnException() {} });
}

test("renders the Infinite Ocean Spyglass home page", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("strict-transport-security"), "max-age=31536000; includeSubDomains");
  assert.equal(response.headers.get("x-content-type-options"), "nosniff");
  assert.equal(response.headers.get("x-frame-options"), "DENY");
  assert.equal(response.headers.get("cross-origin-opener-policy"), "same-origin");
  assert.equal(response.headers.get("cross-origin-resource-policy"), "same-origin");
  assert.match(response.headers.get("permissions-policy"), /camera=\(\)/);
  assert.match(response.headers.get("content-security-policy"), /default-src 'self'.*frame-ancestors 'none'.*script-src 'self'/);
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
  assert.doesNotMatch(signupHtml, /method="post"[^>]*app\.infiniteocean\.net/i);
  assert.doesNotMatch(signupHtml, /name="email"|name="account_name"/i);
  const pricingHtml = await pricing.text();
  assert.match(pricingHtml, /No card required/);
  assert.match(pricingHtml, /app\.infiniteocean\.net\/signup\?offer=team-monthly-v1/);
});

test("proxies only the anonymous published Catalog from the configured account origin", async () => {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("catalog-test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);
  const originalFetch = globalThis.fetch;
  let requested = "";
  globalThis.fetch = async (input) => {
    requested = String(input);
    return new Response(JSON.stringify(publicCatalogFixture), { headers: { "content-type": "application/json" } });
  };
  try {
    const response = await worker.fetch(new Request("https://infiniteocean.net/api/catalog"), { SPYGLASS_ACCOUNT_API_ORIGIN: "https://app.infiniteocean.net", ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } }, { waitUntil() {}, passThroughOnException() {} });
    assert.equal(response.status, 200);
    assert.equal(requested, "https://app.infiniteocean.net/api/v1/catalog/public");
    assert.match(response.headers.get("cache-control"), /stale-while-revalidate/);
    assert.equal(response.headers.get("strict-transport-security"), "max-age=31536000; includeSubDomains");
    assert.equal(response.headers.get("x-content-type-options"), "nosniff");
    assert.equal(response.headers.get("content-security-policy"), null);
    assert.deepEqual(await response.json(), publicCatalogFixture);
  } finally {
    globalThis.fetch = originalFetch;
  }
  const rejected = await worker.fetch(new Request("https://infiniteocean.net/api/catalog"), { SPYGLASS_ACCOUNT_API_ORIGIN: "http://account-api", ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } }, { waitUntil() {}, passThroughOnException() {} });
  assert.equal(rejected.status, 503);
});
