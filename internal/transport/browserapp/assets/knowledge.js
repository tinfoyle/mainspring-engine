(() => {
  "use strict";
  const root = document.getElementById("knowledge-app");
  if (!root) return;
  const accountID = root.dataset.accountId;
  const readOnly = root.dataset.readOnly === "true";
  const base = `/api/v1/accounts/${encodeURIComponent(accountID)}/knowledge`;
  const claims = document.getElementById("knowledge-claims");
  const claimsStatus = document.getElementById("knowledge-claims-status");
  const facts = document.getElementById("knowledge-facts");
  const factsCount = document.getElementById("knowledge-facts-count");
  const detail = document.getElementById("knowledge-detail");
  const commandStatus = document.getElementById("knowledge-command-status");

  const node = (tag, className, text) => { const value = document.createElement(tag); if (className) value.className = className; if (text !== undefined) value.textContent = text; return value; };
  const label = (value) => String(value || "unknown").replaceAll("_", " ");
  const scopeLabel = (scope) => scope?.id ? `${label(scope.kind)} · ${scope.id}` : label(scope?.kind);
  const announce = (message) => { commandStatus.textContent = ""; window.setTimeout(() => { commandStatus.textContent = message; }, 0); };

  async function request(path, options) {
    const response = await fetch(path, { credentials: "same-origin", headers: { Accept: "application/json", ...(options?.headers || {}) }, ...options });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.detail || "Knowledge request failed.");
    return { body, etag: response.headers.get("ETag") || "" };
  }

  function summaryCard(item, kind) {
    const button = node(kind === "claim" ? "button" : "article", `knowledge-card ${kind}`);
    if (kind === "claim") button.type = "button";
    const heading = node("strong", "", item.key);
    const meta = node("span", "", `${scopeLabel(item.scope)} · ${label(item.sensitivity)} · ${kind === "claim" ? `${item.confidence / 10}% confidence` : `revision ${item.revision}`}`);
    button.append(heading, meta);
    if (kind === "claim") button.addEventListener("click", () => loadClaim(item.id));
    return button;
  }

  async function loadQueues() {
    root.setAttribute("aria-busy", "true");
    claimsStatus.textContent = "Loading claims…";
    try {
      const [claimPage, factPage] = await Promise.all([request(`${base}/claims?state=proposed&limit=50`), request(`${base}/facts?limit=50`)]);
      claims.replaceChildren(...claimPage.body.items.map((item) => summaryCard(item, "claim")));
      claimsStatus.textContent = claimPage.body.items.length ? `${claimPage.body.items.length} awaiting review` : "No proposed claims need review.";
      facts.replaceChildren(...factPage.body.items.map((item) => summaryCard(item, "fact")));
      factsCount.textContent = `${factPage.body.items.length} current`;
    } catch (error) {
      claimsStatus.textContent = error.message;
      factsCount.textContent = "Unavailable";
    } finally { root.setAttribute("aria-busy", "false"); }
  }

  async function loadClaim(id) {
    detail.replaceChildren(node("p", "eyebrow", "CLAIM DETAIL"), node("h2", "", "Loading claim…"));
    try {
      const { body, etag } = await request(`${base}/claims/${encodeURIComponent(id)}`);
      const value = node("pre", "knowledge-value", JSON.stringify(body.value, null, 2));
      const citations = node("ul", "knowledge-citations");
      for (const citation of body.citations) citations.append(node("li", "", `${label(citation.relation)} · ${label(citation.evidence_kind)} · ${citation.locator}`));
      const content = [node("p", "eyebrow", "CLAIM DETAIL"), node("h2", "", body.key), node("p", "", `${scopeLabel(body.scope)} · ${label(body.sensitivity)} · ${body.confidence / 10}% confidence`), value, node("h3", "", "Citations"), citations];
      if (!readOnly && body.state === "proposed") content.push(decisionForm(body.id, etag));
      detail.replaceChildren(...content); detail.focus();
    } catch (error) { detail.replaceChildren(node("p", "eyebrow", "CLAIM DETAIL"), node("h2", "", "Claim unavailable"), node("p", "", error.message)); }
  }

  function decisionForm(id, etag) {
    const form = node("form", "knowledge-decision");
    const labelNode = node("label", "", "Decision reason");
    const reason = node("textarea"); reason.name = "reason"; reason.required = true; reason.minLength = 3; reason.maxLength = 1000; reason.rows = 3;
    labelNode.append(reason);
    const accept = node("button", "primary", "Accept claim"); accept.type = "submit"; accept.value = "accept"; accept.name = "decision";
    const reject = node("button", "secondary", "Reject claim"); reject.type = "submit"; reject.value = "reject"; reject.name = "decision";
    form.append(labelNode, accept, reject);
    form.addEventListener("submit", async (event) => {
      event.preventDefault(); const submitter = event.submitter; accept.disabled = reject.disabled = true;
      try {
        await request(`${base}/claims/${encodeURIComponent(id)}/decisions`, { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID(), "If-Match": etag }, body: JSON.stringify({ accept: submitter.value === "accept", reason: reason.value }) });
        announce(`Claim ${submitter.value}ed.`); await loadQueues(); await loadClaim(id);
      } catch (error) { announce(error.message); accept.disabled = reject.disabled = false; }
    });
    return form;
  }

  document.getElementById("knowledge-refresh")?.addEventListener("click", loadQueues);
  loadQueues();
})();
