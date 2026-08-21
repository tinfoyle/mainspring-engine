(() => {
  "use strict";

  const root = document.getElementById("attention-app");
  if (!root) return;

  const accountID = root.dataset.accountId;
  const userID = root.dataset.userId;
  const workAvailable = root.dataset.workAvailable === "true";
  const workReadOnly = root.dataset.workReadOnly === "true";
  const approvalsAvailable = root.dataset.approvalsAvailable === "true";
  const agentsReadOnly = root.dataset.agentsReadOnly === "true";
  const baseURL = `/api/v1/accounts/${encodeURIComponent(accountID)}/attention`;
  const list = document.getElementById("attention-list");
  const detail = document.getElementById("attention-detail");
  const status = document.getElementById("attention-status");
  const count = document.getElementById("attention-result-count");
  const refresh = document.getElementById("attention-refresh");
  const commandStatus = document.getElementById("attention-command-status");
  const pendingOperations = new Map();
  let items = [];
  let filter = "all";
  let selected;
  let queueRequest;
  let detailRequest;

  const labels = {
    information: "Information",
    review: "Work review",
    approval: "Consequential approval",
    action: "Action recovery",
    open: "Open",
    approve: "Approve",
    request_changes: "Request changes",
    reject: "Reject",
    account: "Account",
    work_item: "Work item",
    conversation: "Conversation",
  };

  function node(tag, className, text) {
    const value = document.createElement(tag);
    if (className) value.className = className;
    if (text !== undefined) value.textContent = text;
    return value;
  }

  function label(value) {
    return labels[value] || String(value || "Unknown").replaceAll("_", " ");
  }

  function dateLabel(value) {
    const date = new Date(value);
    if (Number.isNaN(date.valueOf())) return "Time unavailable";
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
  }

  function announce(message) {
    commandStatus.textContent = "";
    window.setTimeout(() => { commandStatus.textContent = message; }, 0);
  }

  function draftKey(kind, id) {
    return `spyglass.attention.decision.v1.${accountID}.${kind}.${id}`;
  }

  function saveDraft(kind, id, form) {
    const draft = {};
    for (const [name, value] of new FormData(form)) draft[name] = String(value);
    try { window.sessionStorage.setItem(draftKey(kind, id), JSON.stringify(draft)); } catch (_) { /* tab storage may be unavailable */ }
  }

  function restoreDraft(kind, id, form) {
    let draft;
    try { draft = JSON.parse(window.sessionStorage.getItem(draftKey(kind, id)) || "null"); } catch (_) { return; }
    if (!draft || typeof draft !== "object") return;
    for (const [name, value] of Object.entries(draft)) {
      const controls = form.elements.namedItem(name);
      if (!controls || typeof value !== "string") continue;
      if (typeof controls.length === "number" && !controls.tagName) {
        for (const control of controls) control.checked = control.value === value;
      } else {
        controls.value = value;
      }
    }
  }

  function clearDraft(kind, id) {
    try { window.sessionStorage.removeItem(draftKey(kind, id)); } catch (_) { /* tab storage may be unavailable */ }
  }

  async function getJSON(url, signal) {
    const response = await fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
    if (!response.ok) {
      let problem = {};
      try { problem = await response.json(); } catch (_) { /* retain safe fallback */ }
      const error = new Error(problem.detail || (response.status === 401 ? "Your session ended. Sign in again to continue." : "Spyglass could not load Your Turn right now."));
      error.status = response.status;
      error.code = problem.code || "attention_unavailable";
      throw error;
    }
    return response.json();
  }

  async function mutateJSON(url, payload, version) {
    const body = JSON.stringify(payload);
    const fingerprint = `${url} ${version} ${body}`;
    let operationID = pendingOperations.get(fingerprint);
    if (!operationID) {
      operationID = crypto.randomUUID();
      pendingOperations.set(fingerprint, operationID);
    }
    const response = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { Accept: "application/json", "Content-Type": "application/json", "Idempotency-Key": operationID, "If-Match": `W/"${version}"` },
      body,
    });
    if (!response.ok) {
      let problem = {};
      try { problem = await response.json(); } catch (_) { /* retain safe fallback */ }
      const error = new Error(problem.detail || "Spyglass could not save this decision.");
      error.status = response.status;
      error.code = problem.code || "attention_unavailable";
      throw error;
    }
    pendingOperations.delete(fingerprint);
    return response.json();
  }

  async function mutateActionJSON(url, payload) {
    const body = payload === undefined ? undefined : JSON.stringify(payload);
    const fingerprint = `${url} ${body || "no-body"}`;
    let operationID = pendingOperations.get(fingerprint);
    if (!operationID) {
      operationID = crypto.randomUUID();
      pendingOperations.set(fingerprint, operationID);
    }
    const headers = { Accept: "application/json", "Idempotency-Key": operationID };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const response = await fetch(url, { method: "POST", credentials: "same-origin", headers, body });
    if (!response.ok) {
      let problem = {};
      try { problem = await response.json(); } catch (_) { /* retain safe fallback */ }
      const error = new Error(problem.detail || "Spyglass could not save this recovery decision.");
      error.status = response.status;
      error.code = problem.code || "action_recovery_unavailable";
      throw error;
    }
    pendingOperations.delete(fingerprint);
    return response.json();
  }

  function endpoint(kind, id) {
    const collection = kind === "information" ? "information-requests" : kind === "review" ? "work-reviews" : kind === "action" ? "actions" : "approvals";
    return `${baseURL}/${collection}${id ? `/${encodeURIComponent(id)}` : ""}`;
  }

  function summary(item) {
    if (item._kind === "information") return item.question;
    if (item._kind === "review") return item.question;
    return item.capability;
  }

  function context(item) {
    if (item._kind === "information") return `Work ${item.parent_work_item_id}`;
    if (item._kind === "review") return `Work ${item.work_item_id} · version ${item.work_version}`;
    if (item._kind === "approval") return item.work_item_id ? `Work ${item.work_item_id}` : `Invocation ${item.invocation_id}`;
    return `${item.executor_id} v${item.executor_version} · attempt ${item.attempt_count}`;
  }

  function card(item) {
    const button = node("button", `attention-card attention-${item._kind}`);
    button.type = "button";
    button.dataset.attentionId = item.id;
    button.dataset.attentionKind = item._kind;
    button.setAttribute("aria-pressed", String(selected && selected.id === item.id && selected._kind === item._kind));
    button.setAttribute("aria-label", `Open ${label(item._kind)}: ${summary(item)}`);
    const marker = node("i", "attention-marker", item._kind === "information" ? "?" : item._kind === "review" ? "✓" : item._kind === "action" ? "↻" : "!");
    marker.setAttribute("aria-hidden", "true");
    const body = node("span", "attention-card-body");
    body.append(node("small", "attention-kind", label(item._kind)), node("strong", "", summary(item)), node("span", "", context(item)), node("time", "", `Updated ${dateLabel(item.updated_at)}`));
    button.append(marker, body, node("span", "attention-state", label(item.state)));
    button.addEventListener("click", () => loadDetail(item));
    return button;
  }

  function renderQueue() {
    const visible = items.filter((item) => filter === "all" || item._kind === filter);
    list.replaceChildren(...visible.map(card));
    count.textContent = `${visible.length} open`;
    status.hidden = visible.length > 0;
    status.classList.remove("error");
    status.textContent = visible.length ? "" : "Nothing in this view needs your attention.";
    for (const kind of ["information", "review", "approval", "action"]) {
      const target = root.querySelector(`[data-attention-summary="${kind}"]`);
      if (target) target.textContent = String(items.filter((item) => item._kind === kind).length);
    }
    root.querySelector('[data-attention-summary="all"]').textContent = String(items.length);
  }

  async function loadQueue({ announceResult = false } = {}) {
    if (queueRequest) queueRequest.abort();
    queueRequest = new AbortController();
    root.setAttribute("aria-busy", "true");
    status.hidden = false;
    status.classList.remove("error");
    status.textContent = "Loading Your Turn…";
    const sources = [];
    if (workAvailable) {
      sources.push(["information", `${endpoint("information")}?state=open&limit=100`]);
      sources.push(["review", `${endpoint("review")}?state=open&reviewer_id=${encodeURIComponent(userID)}&limit=100`]);
    }
    if (approvalsAvailable) {
      sources.push(["approval", `${endpoint("approval")}?state=open&limit=100`]);
      sources.push(["action", `${endpoint("action")}?state=unknown&limit=100`]);
      sources.push(["action", `${endpoint("action")}?state=manual_resolution&limit=100`]);
    }
    try {
      const results = await Promise.all(sources.map(async ([kind, url]) => ({ kind, page: await getJSON(url, queueRequest.signal) })));
      items = results.flatMap(({ kind, page }) => (page.items || []).map((item) => ({ ...item, id: kind === "action" ? item.operation_id : item.id, _kind: kind })))
        .sort((left, right) => String(left.updated_at).localeCompare(String(right.updated_at)));
      renderQueue();
      if (announceResult) announce(`Your Turn refreshed. ${items.length} open ${items.length === 1 ? "item" : "items"}.`);
    } catch (error) {
      if (error.name === "AbortError") return;
      items = [];
      list.replaceChildren();
      count.textContent = "Unavailable";
      status.hidden = false;
      status.classList.add("error");
      status.textContent = error.message;
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }

  function detailField(term, value, className = "") {
    const wrapper = node("div", className);
    wrapper.append(node("dt", "", term), node("dd", "", value || "Not set"));
    return wrapper;
  }

  function informationForm(item) {
    const form = node("form", "attention-decision-form");
    form.dataset.kind = "information";
    form.append(node("h3", "", "Supply the requested fact"));
    const factID = node("input");
    factID.name = "fact_id";
    factID.required = true;
    factID.setAttribute("aria-describedby", "attention-draft-note");
    const factLabel = node("label", "", "Fact identifier");
    factLabel.append(factID);
    const factVersion = node("input");
    factVersion.name = "fact_version";
    factVersion.type = "number";
    factVersion.min = "1";
    factVersion.required = true;
    const versionLabel = node("label", "", "Fact version");
    versionLabel.append(factVersion);
    const submit = node("button", "primary", "Submit answer");
    submit.type = "submit";
    form.append(factLabel, versionLabel, draftNote(), formError(), submit);
    bindDecisionForm(form, item, (values) => ({
      fact_id: String(values.get("fact_id") || ""),
      fact_version: Number(values.get("fact_version")),
      requirement: item.requirement,
    }), "answers");
    return form;
  }

  function decisionForm(item) {
    const form = node("form", "attention-decision-form");
    form.dataset.kind = item._kind;
    form.append(node("h3", "", item._kind === "review" ? "Record your Work review" : "Decide this consequential action"));
    const choices = item._kind === "review" ? [["approve", "Approve"], ["request_changes", "Request changes"]] : [["approve", "Approve action"], ["reject", "Reject action"]];
    const fieldset = node("fieldset");
    fieldset.append(node("legend", "", "Decision"));
    for (const [value, text] of choices) {
      const input = node("input");
      input.type = "radio";
      input.name = "decision";
      input.value = value;
      input.required = true;
      const choice = node("label", "attention-choice", text);
      choice.prepend(input);
      fieldset.append(choice);
    }
    const reason = node("textarea");
    reason.name = "reason";
    reason.minLength = 3;
    reason.maxLength = 1000;
    reason.rows = 4;
    reason.required = true;
    reason.setAttribute("aria-describedby", "attention-draft-note");
    const reasonLabel = node("label", "", "Decision reason");
    reasonLabel.append(reason);
    const submit = node("button", "primary", "Record decision");
    submit.type = "submit";
    form.append(fieldset, reasonLabel, draftNote(), formError(), submit);
    bindDecisionForm(form, item, (values) => ({ decision: String(values.get("decision") || ""), reason: String(values.get("reason") || "").trim() }), "decisions");
    return form;
  }

  function actionResolutionForm(item) {
    const form = node("form", "attention-decision-form");
    form.dataset.kind = "action";
    form.append(node("h3", "", "Request a manual outcome"));
    const fieldset = node("fieldset");
    fieldset.append(node("legend", "", "Observed provider outcome"));
    for (const [value, text] of [["succeeded", "Succeeded"], ["failed", "Failed"]]) {
      const input = node("input");
      input.type = "radio";
      input.name = "outcome";
      input.value = value;
      input.required = true;
      const choice = node("label", "attention-choice", text);
      choice.prepend(input);
      fieldset.append(choice);
    }
    const reason = node("textarea");
    reason.name = "reason";
    reason.minLength = 3;
    reason.maxLength = 1000;
    reason.rows = 4;
    reason.required = true;
    reason.setAttribute("aria-describedby", "attention-draft-note");
    const reasonLabel = node("label", "", "Evidence-based reason (stored as a digest here)");
    reasonLabel.append(reason);
    const submit = node("button", "primary", "Request resolution");
    submit.type = "submit";
    form.append(fieldset, reasonLabel, draftNote(), formError(), submit);
    restoreDraft("action", item.id, form);
    form.addEventListener("input", () => saveDraft("action", item.id, form));
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!form.reportValidity()) return;
      saveDraft("action", item.id, form);
      const errorView = form.querySelector(".work-form-error");
      const submitButton = form.querySelector('button[type="submit"]');
      errorView.hidden = true;
      submitButton.disabled = true;
      const values = new FormData(form);
      try {
        const updated = await mutateActionJSON(`${endpoint("action", item.id)}/resolution-requests`, { outcome: String(values.get("outcome") || ""), reason: String(values.get("reason") || "").trim() });
        clearDraft("action", item.id);
        announce("Manual resolution requested. A different eligible operator must confirm it.");
        await loadQueue();
        renderDetail({ ...updated, id: updated.operation_id, _kind: "action" });
      } catch (error) {
        errorView.textContent = error.message;
        errorView.hidden = false;
      } finally {
        submitButton.disabled = false;
      }
    });
    return form;
  }

  function actionConfirmation(item) {
    const section = node("section", "attention-decision-form");
    section.append(node("h3", "", "Independent confirmation required"));
    if (item.resolution.requested_by_user_id === userID) {
      section.append(node("p", "attention-read-only", "A different eligible Owner or Administrator must confirm this outcome."));
      return section;
    }
    const button = node("button", "primary", `Confirm ${label(item.resolution.requested_outcome)}`);
    button.type = "button";
    const errorView = formError();
    button.addEventListener("click", async () => {
      button.disabled = true;
      errorView.hidden = true;
      try {
        const updated = await mutateActionJSON(`${endpoint("action", item.id)}/resolutions/${encodeURIComponent(item.resolution.id)}/confirmations`);
        announce("Manual action outcome confirmed.");
        await loadQueue();
        renderDetail({ ...updated, id: updated.operation_id, _kind: "action" });
      } catch (error) {
        errorView.textContent = error.message;
        errorView.hidden = false;
      } finally {
        button.disabled = false;
      }
    });
    section.append(errorView, button);
    return section;
  }

  function draftNote() {
    return node("p", "work-draft-note", "This decision draft stays in this browser tab until it is accepted.");
  }

  function formError() {
    const error = node("p", "work-form-error");
    error.setAttribute("role", "alert");
    error.hidden = true;
    return error;
  }

  function bindDecisionForm(form, item, payload, action) {
    const note = form.querySelector(".work-draft-note");
    note.id = "attention-draft-note";
    restoreDraft(item._kind, item.id, form);
    form.addEventListener("input", () => saveDraft(item._kind, item.id, form));
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!form.reportValidity()) return;
      saveDraft(item._kind, item.id, form);
      const errorView = form.querySelector(".work-form-error");
      const submit = form.querySelector('button[type="submit"]');
      errorView.hidden = true;
      submit.disabled = true;
      try {
        const updated = await mutateJSON(`${endpoint(item._kind, item.id)}/${action}`, payload(new FormData(form)), item.version);
        clearDraft(item._kind, item.id);
        announce(`${label(item._kind)} completed.`);
        selected = undefined;
        await loadQueue();
        renderDetail({ ...updated, _kind: item._kind });
      } catch (error) {
        errorView.textContent = error.message;
        errorView.hidden = false;
        if (error.code === "attention_version_conflict" || error.status === 412) {
          announce("This item changed. The latest version is loading; your draft is preserved.");
          await loadDetail(item);
        }
      } finally {
        submit.disabled = false;
      }
    });
  }

  function renderDetail(item) {
    selected = item;
    for (const cardElement of list.querySelectorAll(".attention-card")) {
      cardElement.setAttribute("aria-pressed", String(cardElement.dataset.attentionId === item.id && cardElement.dataset.attentionKind === item._kind));
    }
    const header = node("header");
    const heading = node("div");
    heading.append(node("p", "eyebrow", label(item._kind)), node("h2", "", summary(item)));
    header.append(heading, node("span", "attention-state", label(item.state)));
    const facts = node("dl", "attention-facts");
    if (item._kind === "information") {
      facts.append(detailField("Required fact", item.requirement.key), detailField("Scope", `${label(item.requirement.scope)}${item.requirement.scope_id ? ` · ${item.requirement.scope_id}` : ""}`), detailField("Parent Work", item.parent_work_item_id), detailField("Requested", dateLabel(item.created_at)));
    } else if (item._kind === "review") {
      facts.append(detailField("Work item", item.work_item_id), detailField("Work version", String(item.work_version)), detailField("Proposal SHA-256", item.proposal_sha256, "attention-digest"), detailField("Requested", dateLabel(item.created_at)));
    } else if (item._kind === "approval") {
      facts.append(detailField("Capability", item.capability), detailField("Operation", item.operation_id), detailField("Invocation", item.invocation_id), detailField("Evidence SHA-256", item.evidence_sha256, "attention-digest"), detailField("Policy version", String(item.policy_version)), detailField("Expires", dateLabel(item.expires_at)));
    } else {
      facts.append(detailField("Capability", item.capability), detailField("Operation", item.operation_id), detailField("Approval", item.approval_id), detailField("Invocation", item.invocation_id), detailField("Executor", `${item.executor_id} v${item.executor_version}`), detailField("Frozen policy", String(item.policy_version)), detailField("Attempts", String(item.attempt_count)), detailField("Stable error", item.last_error_code || "None"), detailField("Started", dateLabel(item.started_at)), detailField("Updated", dateLabel(item.updated_at)));
      if (item.resolution) facts.append(detailField("Requested outcome", label(item.resolution.requested_outcome)), detailField("Reason SHA-256", item.resolution.reason_sha256, "attention-digest"), detailField("Requested by", item.resolution.requested_by_user_id), detailField("Resolution state", label(item.resolution.state)));
    }
    const children = [header, facts];
    if (item._kind === "approval") {
      const payload = node("section", "attention-payload");
      payload.append(node("h3", "", "Exact proposed payload"), node("pre", "", JSON.stringify(item.payload, null, 2)));
      children.push(payload);
    }
    const writable = item._kind === "approval" || item._kind === "action" ? !agentsReadOnly : !workReadOnly;
    if (item._kind === "action" && item.state === "unknown" && writable) children.push(actionResolutionForm(item));
    if (item._kind === "action" && item.state === "manual_resolution" && item.resolution?.state === "pending" && writable) children.push(actionConfirmation(item));
    if (item._kind === "action" && (item.state === "unknown" || item.state === "manual_resolution") && !writable) children.push(node("p", "attention-read-only", "The Agents package is read-only. You can inspect recovery status but cannot submit a resolution."));
    if (item.state === "open" && writable) children.push(item._kind === "information" ? informationForm(item) : decisionForm(item));
    if (item.state === "open" && !writable) children.push(node("p", "attention-read-only", "This package is read-only. You can inspect the item but cannot submit a decision."));
    detail.replaceChildren(...children);
    detail.setAttribute("aria-busy", "false");
    detail.focus();
  }

  async function loadDetail(item) {
    if (detailRequest) detailRequest.abort();
    detailRequest = new AbortController();
    detail.setAttribute("aria-busy", "true");
    detail.replaceChildren(node("p", "attention-status", "Loading exact decision detail…"));
    try {
      const value = await getJSON(endpoint(item._kind, item.id), detailRequest.signal);
      renderDetail({ ...value, _kind: item._kind });
    } catch (error) {
      if (error.name === "AbortError") return;
      detail.replaceChildren(node("p", "attention-status error", error.message));
      detail.setAttribute("aria-busy", "false");
      detail.focus();
    }
  }

  for (const button of root.querySelectorAll("[data-attention-filter]")) {
    button.addEventListener("click", () => {
      filter = button.dataset.attentionFilter;
      for (const peer of root.querySelectorAll("[data-attention-filter]")) peer.setAttribute("aria-pressed", String(peer === button));
      renderQueue();
      list.querySelector(".attention-card")?.focus();
    });
  }

  list.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const cards = [...list.querySelectorAll(".attention-card")];
    const current = cards.indexOf(document.activeElement);
    if (current < 0) return;
    event.preventDefault();
    cards[(current + (event.key === "ArrowDown" ? 1 : -1) + cards.length) % cards.length].focus();
  });

  refresh.addEventListener("click", () => loadQueue({ announceResult: true }));
  loadQueue();
})();
