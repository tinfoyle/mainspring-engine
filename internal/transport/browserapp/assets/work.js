(() => {
  "use strict";

  const root = document.getElementById("work-app");
  if (!root) return;

  const accountID = root.dataset.accountId;
  const readOnly = root.dataset.readOnly === "true";
  const baseURL = `/api/v1/accounts/${encodeURIComponent(accountID)}/work-items`;
  const form = document.getElementById("work-filters");
  const list = document.getElementById("work-list");
  const status = document.getElementById("work-status");
  const count = document.getElementById("work-result-count");
  const more = document.getElementById("work-more");
  const detail = document.getElementById("work-detail");
  const createOpen = document.getElementById("work-create-open");
  const createDialog = document.getElementById("work-create-dialog");
  const createForm = document.getElementById("work-create-form");
  const createError = document.getElementById("work-create-error");
  const pendingOperations = new Map();
  let nextCursor = "";
  let loadedCount = 0;
  let listRequest;
  let detailRequest;

  const labels = {
    todo: "To-do",
    ticket: "Ticket",
    open: "Open",
    in_progress: "In progress",
    waiting: "Waiting",
    done: "Done",
    canceled: "Canceled",
    low: "Low",
    normal: "Normal",
    high: "High",
    urgent: "Urgent",
    user: "Person",
    persona: "Agent",
    shared: "Shared",
    external: "External",
    manual: "Manual",
    baseline: "Baseline",
    schedule: "Schedule",
    conversation: "Conversation",
    run: "Agent run",
    system: "System",
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
    if (!value) return "No due date";
    const date = new Date(value);
    if (Number.isNaN(date.valueOf())) return "No due date";
    return `Due ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(date)}`;
  }

  async function getJSON(url, signal) {
    const response = await fetch(url, {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      signal,
    });
    if (!response.ok) {
      let message = "Spyglass could not load Work right now.";
      try {
        const problem = await response.json();
        if (problem.detail) message = problem.detail;
      } catch (_) {
        // The stable fallback is safer than showing an upstream response body.
      }
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
    return response.json();
  }

  async function mutateJSON(url, method, payload, version) {
    const body = JSON.stringify(payload);
    const fingerprint = `${method} ${url} ${version || ""} ${body}`;
    let operationID = pendingOperations.get(fingerprint);
    if (!operationID) {
      operationID = crypto.randomUUID();
      pendingOperations.set(fingerprint, operationID);
    }
    const headers = { Accept: "application/json", "Content-Type": "application/json", "Idempotency-Key": operationID };
    if (version) headers["If-Match"] = `W/"${version}"`;
    const response = await fetch(url, { method, credentials: "same-origin", headers, body });
    if (!response.ok) {
      let problem = {};
      try { problem = await response.json(); } catch (_) { /* retain the safe fallback */ }
      const error = new Error(problem.detail || "Spyglass could not save this Work command.");
      error.status = response.status;
      error.code = problem.code || "work_unavailable";
      throw error;
    }
    pendingOperations.delete(fingerprint);
    return response.json();
  }

  function queryURL(cursor) {
    const values = new FormData(form);
    const query = new URLSearchParams();
    for (const name of ["q", "state", "kind"]) {
      const value = String(values.get(name) || "").trim();
      if (value) query.set(name, value);
    }
    query.set("limit", "30");
    if (cursor) query.set("cursor", cursor);
    return `${baseURL}?${query}`;
  }

  function workCard(item) {
    const card = node("button", "work-card");
    card.type = "button";
    card.dataset.workItemId = item.id;
    card.setAttribute("aria-label", `Open work item ${item.number}: ${item.title}`);
    card.setAttribute("aria-pressed", "false");

    const signal = node("i", `work-signal state-${item.state}`);
    signal.setAttribute("aria-hidden", "true");
    const body = node("span", "work-card-body");
    const heading = node("span", "work-card-heading");
    heading.append(node("small", "work-number", `#${String(item.number).padStart(4, "0")}`));
    heading.append(node("strong", "", item.title));
    const meta = node("span", "work-card-meta");
    meta.append(node("em", `work-pill priority-${item.priority}`, label(item.priority)));
    meta.append(node("span", "", label(item.kind)));
    meta.append(node("span", "", label(item.assignment && item.assignment.responsibility)));
    meta.append(node("span", "", dateLabel(item.due_at)));
    body.append(heading, meta);
    card.append(signal, body, node("span", `work-state state-${item.state}`, label(item.state)));
    card.addEventListener("click", () => loadDetail(item.id, card));
    return card;
  }

  async function loadSummary() {
    try {
      const summary = await getJSON(`${baseURL}/summary`);
      for (const [key, value] of Object.entries(summary)) {
        const target = root.querySelector(`[data-summary="${key}"]`);
        if (target) target.textContent = String(value);
      }
    } catch (_) {
      for (const target of root.querySelectorAll("[data-summary]")) target.textContent = "—";
    }
  }

  async function loadList(append) {
    if (listRequest) listRequest.abort();
    listRequest = new AbortController();
    status.hidden = false;
    status.classList.remove("error");
    status.textContent = append ? "Loading more work…" : "Loading Account work…";
    more.hidden = true;
    root.setAttribute("aria-busy", "true");
    try {
      const page = await getJSON(queryURL(append ? nextCursor : ""), listRequest.signal);
      if (!append) {
        list.replaceChildren();
        loadedCount = 0;
      }
      for (const item of page.items || []) list.append(workCard(item));
      loadedCount += (page.items || []).length;
      nextCursor = page.next_cursor || "";
      count.textContent = `${loadedCount} loaded`;
      if (loadedCount === 0) {
        status.textContent = "No work matches this view.";
      } else {
        status.hidden = true;
      }
      more.hidden = !nextCursor;
    } catch (error) {
      if (error.name === "AbortError") return;
      if (!append) list.replaceChildren();
      status.hidden = false;
      status.classList.add("error");
      status.textContent = error.status === 403
        ? "This Account's Work access is no longer available. Refresh to update the package view."
        : error.message;
      count.textContent = "Unavailable";
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }

  function detailRow(term, value) {
    const row = node("div", "work-detail-row");
    row.append(node("dt", "", term), node("dd", "", value));
    return row;
  }

  function renderDetail(item, children) {
    const header = node("header", "work-detail-head");
    const title = node("div");
    title.append(node("p", "eyebrow", `${label(item.kind).toUpperCase()} · #${String(item.number).padStart(4, "0")}`));
    title.append(node("h2", "", item.title));
    header.append(title, node("span", `work-state state-${item.state}`, label(item.state)));
    const description = node("p", "work-description", item.description || "No description has been added.");
    const facts = node("dl", "work-detail-facts");
    facts.append(
      detailRow("Priority", label(item.priority)),
      detailRow("Responsibility", label(item.assignment && item.assignment.responsibility)),
      detailRow("Origin", label(item.provenance && item.provenance.source)),
      detailRow("Due", dateLabel(item.due_at).replace(/^Due /, "")),
      detailRow("Updated", new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(item.updated_at)))
    );
    const childSection = node("section", "work-children");
    childSection.append(node("h3", "", `Direct work · ${children.length}`));
    if (children.length === 0) {
      childSection.append(node("p", "", "No direct child items."));
    } else {
      for (const child of children) childSection.append(workCard(child));
    }
    const sections = [header, description, facts];
    if (!readOnly) sections.push(workActions(item, children));
    sections.push(childSection);
    detail.replaceChildren(...sections);
  }

  function workActions(item, children) {
    const section = node("section", "work-actions");
    const message = node("p", "work-action-message");
    message.hidden = true;
    const transitions = {
      open: [["in_progress", "Start"], ["canceled", "Cancel"]],
      in_progress: [["waiting", "Mark waiting"], ["done", "Complete"], ["canceled", "Cancel"]],
      waiting: [["in_progress", "Resume"], ["canceled", "Cancel"]],
      done: [["open", "Reopen"]],
    }[item.state] || [];
    section.append(node("strong", "", "Move this work"));
    const controls = node("div", "work-action-controls");
    for (const [state, text] of transitions) {
      const button = node("button", state === "done" ? "primary" : "secondary", text);
      button.type = "button";
      button.addEventListener("click", async () => {
        let reason = "";
        if (["waiting", "canceled", "open"].includes(state)) {
          reason = String(window.prompt(state === "open" ? "Why is this work reopening?" : "Add the operational reason:") || "").trim();
          if (!reason) return;
        }
        for (const control of controls.querySelectorAll("button")) control.disabled = true;
        message.hidden = true;
        try {
          const updated = await mutateJSON(`${baseURL}/${encodeURIComponent(item.id)}/transitions`, "POST", { to: state, ...(reason ? { reason } : {}) }, item.version);
          renderDetail(updated, children);
          loadSummary();
          loadList(false);
        } catch (error) {
          message.textContent = error.status === 412 ? "This item changed. Reloading the current version…" : error.message;
          message.hidden = false;
          if (error.status === 412) setTimeout(() => loadDetail(item.id), 350);
          for (const control of controls.querySelectorAll("button")) control.disabled = false;
        }
      });
      controls.append(button);
    }
    if (transitions.length === 0) controls.append(node("span", "", "No further lifecycle action is available."));
    section.append(controls, message);
    return section;
  }

  async function loadDetail(itemID, card) {
    if (detailRequest) detailRequest.abort();
    detailRequest = new AbortController();
    for (const current of list.querySelectorAll(".work-card")) {
      const selected = current === card;
      current.classList.toggle("selected", selected);
      current.setAttribute("aria-pressed", String(selected));
    }
    detail.setAttribute("aria-busy", "true");
    detail.replaceChildren(node("div", "work-detail-empty", "Loading work detail…"));
    try {
      const [item, childPage] = await Promise.all([
        getJSON(`${baseURL}/${encodeURIComponent(itemID)}`, detailRequest.signal),
        getJSON(`${baseURL}/${encodeURIComponent(itemID)}/children?limit=20`, detailRequest.signal),
      ]);
      renderDetail(item, childPage.items || []);
    } catch (error) {
      if (error.name === "AbortError") return;
      const failed = node("div", "work-detail-empty");
      failed.append(node("p", "eyebrow", "WORK DETAIL"), node("h2", "", "Detail unavailable"), node("p", "", error.message));
      detail.replaceChildren(failed);
    } finally {
      detail.setAttribute("aria-busy", "false");
    }
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    nextCursor = "";
    loadList(false);
  });
  more.addEventListener("click", () => loadList(true));
  if (createOpen && createDialog && createForm) {
    const closeCreate = () => {
      createDialog.close();
      createError.hidden = true;
    };
    createDialog.addEventListener("close", () => createOpen.focus());
    createOpen.addEventListener("click", () => {
      createError.hidden = true;
      createDialog.showModal();
      createForm.elements.title.focus();
    });
    document.getElementById("work-create-close").addEventListener("click", closeCreate);
    document.getElementById("work-create-cancel").addEventListener("click", closeCreate);
    createForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const values = new FormData(createForm);
      const submit = createForm.querySelector('button[type="submit"]');
      const payload = {
        kind: String(values.get("kind")),
        title: String(values.get("title") || "").trim(),
        description: String(values.get("description") || "").trim(),
        priority: String(values.get("priority")),
        assignment: { responsibility: String(values.get("responsibility")) },
      };
      submit.disabled = true;
      createError.hidden = true;
      try {
        const item = await mutateJSON(baseURL, "POST", payload);
        createForm.reset();
        closeCreate();
        await Promise.all([loadSummary(), loadList(false)]);
        loadDetail(item.id);
      } catch (error) {
        createError.textContent = error.message;
        createError.hidden = false;
      } finally {
        submit.disabled = false;
      }
    });
  }
  loadSummary();
  loadList(false);
})();
