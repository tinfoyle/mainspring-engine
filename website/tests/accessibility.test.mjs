import assert from "node:assert/strict";
import test from "node:test";
import axe from "axe-core";
import { JSDOM } from "jsdom";

const routes = ["/", "/about", "/packages", "/pricing", "/privacy", "/product", "/security", "/signup", "/terms"];

async function render(path) {
  return fetch(`${process.env.TEST_ORIGIN}${path}`, { headers: { accept: "text/html" } });
}

function formatViolations(violations) {
  return violations.map((violation) => {
    const nodes = violation.nodes.map((node) => `    ${node.target.join(" ")} — ${node.failureSummary}`).join("\n");
    return `${violation.id}: ${violation.help}\n${nodes}`;
  }).join("\n\n");
}

for (const route of routes) {
  test(`${route} has one accessible page frame`, async () => {
    const response = await render(route);
    assert.equal(response.status, 200);
    const dom = new JSDOM(await response.text(), { url: `https://infiniteocean.net${route}`, runScripts: "outside-only" });
    const { document } = dom.window;

    assert.equal(document.documentElement.lang, "en");
    assert.ok(document.title.trim(), "document title must not be empty");
    assert.equal(document.querySelectorAll("header.site-header").length, 1);
    assert.equal(document.querySelectorAll("main").length, 1);
    assert.equal(document.querySelectorAll("footer.site-footer").length, 1);
    assert.equal(document.querySelectorAll("h1").length, 1);

    const main = document.querySelector("main");
    assert.equal(main?.id, "main-content");
    assert.equal(main?.getAttribute("tabindex"), "-1");
    assert.equal(main?.querySelector("header.site-header"), null);
    assert.equal(main?.querySelector("footer.site-footer"), null);

    const firstLink = document.querySelector("body a");
    assert.equal(firstLink?.classList.contains("skip-link"), true);
    assert.equal(firstLink?.getAttribute("href"), "#main-content");

    const ids = [...document.querySelectorAll("[id]")].map((element) => element.id);
    assert.equal(new Set(ids).size, ids.length, "element IDs must be unique");

    dom.window.eval(axe.source);
    const results = await dom.window.axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
      rules: {
        // jsdom has no layout or computed pixel colors. Contrast is certified in the
        // real-browser/manual matrix instead of being silently approximated here.
        "color-contrast": { enabled: false },
      },
    });
    assert.equal(results.violations.length, 0, `Automated accessibility violations on ${route}:\n${formatViolations(results.violations)}`);
    dom.window.close();
  });
}
