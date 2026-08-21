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
  const commandStatus = document.getElementById("work-command-status");
  const transitionDialog = document.getElementById("work-transition-dialog");
  const transitionForm = document.getElementById("work-transition-form");
  const transitionError = document.getElementById("work-transition-error");
  const assignmentDialog = document.getElementById("work-assignment-dialog");
  const assignmentForm = document.getElementById("work-assignment-form");
  const assignmentError = document.getElementById("work-assignment-error");
  const draftKey = `spyglass.work.create.v1.${accountID}`;
  const pendingOperations = new Map();
  const personas = new Map();
  let nextCursor = "";
  let loadedCount = 0;
  let listRequest;
  let detailRequest;
  let transitionCommand;
  let assignmentCommand;

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

  function announce(message) {
    if (!commandStatus) return;
    commandStatus.textContent = "";
    window.setTimeout(() => { commandStatus.textContent = message; }, 0);
  }

  function assignmentLabel(assignment) {
    if (!assignment) return "Unknown";
    if (assignment.responsibility === "persona") return personas.get(assignment.persona_id) || "Agent";
    if (assignment.responsibility === "external") return assignment.external_ref || "External";
    if (assignment.responsibility === "user") return "Assigned to you";
    return label(assignment.responsibility);
  }

  function assignmentPayload(values) {
    const responsibility = String(values.get("responsibility") || "");
    const assignment = { responsibility };
    if (responsibility === "persona") assignment.persona_id = String(values.get("persona_id") || "");
    if (responsibility === "external") assignment.external_ref = String(values.get("external_ref") || "").trim();
    return assignment;
  }

  function syncAssignmentFields(formElement, prefix) {
    if (!formElement) return;
    const responsibility = String(new FormData(formElement).get("responsibility") || "shared");
    for (const kind of ["external", "persona"]) {
      const field = document.getElementById(`${prefix}-${kind}-field`);
      if (!field) continue;
      const input = field.querySelector("input,select");
      const active = responsibility === kind;
      field.hidden = !active;
      input.disabled = !active;
      input.required = active;
    }
  }

  function saveCreateDraft() {
    if (!createForm) return;
    const values = new FormData(createForm);
    const draft = {};
    for (const name of ["title", "description", "kind", "priority", "responsibility", "external_ref", "persona_id"]) {
      draft[name] = String(values.get(name) || "");
    }
    try { window.sessionStorage.setItem(draftKey, JSON.stringify(draft)); } catch (_) { /* browser storage can be unavailable */ }
  }

  function restoreCreateDraft() {
    if (!createForm) return;
    let draft;
    try { draft = JSON.parse(window.sessionStorage.getItem(draftKey) || "null"); } catch (_) { return; }
    if (!draft || typeof draft !== "object") return;
    for (const name of ["title", "description", "kind", "priority", "responsibility", "external_ref", "persona_id"]) {
      const control = createForm.elements.namedItem(name);
      if (control && typeof draft[name] === "string") control.value = draft[name];
    }
    syncAssignmentFields(createForm, "work-create");
  }

  function clearCreateDraft() {
    try { window.sessionStorage.removeItem(draftKey); } catch (_) { /* browser storage can be unavailable */ }
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

  async function loadPersonas() {
    if (readOnly || !createForm) return;
    try {
      const boardrooms = await getJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/agent-boardrooms`);
      const pages = await Promise.all((boardrooms.items || []).filter((room) => room.state === "active").map((room) =>
        getJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/agent-boardrooms/${encodeURIComponent(room.id)}/personas`)
      ));
      for (const page of pages) {
        for (const persona of page.items || []) {
          if (persona.state === "active") personas.set(persona.id, `${persona.name} · ${persona.role}`);
        }
      }
      for (const selectID of ["work-create-persona", "work-assignment-persona"]) {
        const select = document.getElementById(selectID);
        if (!select) continue;
        select.replaceChildren(...[...personas].sort((left, right) => left[1].localeCompare(right[1])).map(([id, name]) => {
          const option = node("option", "", name);
          option.value = id;
          return option;
        }));
      }
      for (const selectID of ["work-create-responsibility", "work-assignment-responsibility"]) {
        const option = document.getElementById(selectID)?.querySelector('option[value="persona"]');
        if (!option) continue;
        option.disabled = personas.size === 0;
        option.textContent = personas.size === 0 ? "Agent (no active Persona)" : "Agent Persona";
      }
      restoreCreateDraft();
    } catch (_) {
      // Work remains usable when the Account has no readable Agents package.
    }
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
      detailRow("Responsibility", assignmentLabel(item.assignment)),
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
    const transitions = {
      open: [["in_progress", "Start"], ["canceled", "Cancel"]],
      in_progress: [["waiting", "Mark waiting"], ["done", "Complete"], ["canceled", "Cancel"]],
      waiting: [["in_progress", "Resume"], ["canceled", "Cancel"]],
      done: [["open", "Reopen"]],
    }[item.state] || [];

    const assignmentGroup = node("div", "work-action-group");
    assignmentGroup.append(node("strong", "", "Responsibility"));
    const assignmentControls = node("div", "work-action-controls");
    const assignmentButton = node("button", "secondary", "Edit assignment");
    assignmentButton.type = "button";
    assignmentButton.addEventListener("click", () => {
      assignmentCommand = { item, children, focus: assignmentButton };
      assignmentForm.reset();
      assignmentForm.elements.responsibility.value = item.assignment.responsibility;
      assignmentForm.elements.external_ref.value = item.assignment.external_ref || "";
      if (item.assignment.persona_id) {
        const select = assignmentForm.elements.persona_id;
        if (![...select.options].some((option) => option.value === item.assignment.persona_id)) {
          const current = node("option", "", "Current Agent assignment (not available for a new assignment)");
          current.value = item.assignment.persona_id;
          current.disabled = true;
          select.append(current);
        }
        select.value = item.assignment.persona_id;
      }
      syncAssignmentFields(assignmentForm, "work-assignment");
      assignmentError.hidden = true;
      assignmentDialog.showModal();
      assignmentForm.elements.responsibility.focus();
    });
    assignmentControls.append(assignmentButton);
    assignmentGroup.append(assignmentControls);

    const transitionGroup = node("div", "work-action-group");
    transitionGroup.append(node("strong", "", "Move this work"));
    const controls = node("div", "work-action-controls");
    for (const [state, text] of transitions) {
      const button = node("button", state === "done" ? "primary" : "secondary", text);
      button.type = "button";
      button.addEventListener("click", () => {
        transitionCommand = { item, children, focus: button, state, text };
        transitionForm.reset();
        transitionError.hidden = true;
        document.getElementById("work-transition-title").textContent = `${text} work`;
        document.getElementById("work-transition-context").textContent = `#${String(item.number).padStart(4, "0")} · ${item.title}`;
        transitionDialog.showModal();
        transitionForm.elements.reason.focus();
      });
      controls.append(button);
    }
    if (transitions.length === 0) controls.append(node("span", "", "No further lifecycle action is available."));
    transitionGroup.append(controls);
    section.append(assignmentGroup, transitionGroup);
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
    createForm.elements.responsibility.addEventListener("change", () => {
      syncAssignmentFields(createForm, "work-create");
      saveCreateDraft();
    });
    createForm.addEventListener("input", saveCreateDraft);
    createForm.addEventListener("change", saveCreateDraft);
    createForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const values = new FormData(createForm);
      const submit = createForm.querySelector('button[type="submit"]');
      const payload = {
        kind: String(values.get("kind")),
        title: String(values.get("title") || "").trim(),
        description: String(values.get("description") || "").trim(),
        priority: String(values.get("priority")),
        assignment: assignmentPayload(values),
      };
      submit.disabled = true;
      createError.hidden = true;
      try {
        const item = await mutateJSON(baseURL, "POST", payload);
        clearCreateDraft();
        createForm.reset();
        syncAssignmentFields(createForm, "work-create");
        closeCreate();
        announce(`Created work item ${item.number}: ${item.title}.`);
        await Promise.all([loadSummary(), loadList(false)]);
        loadDetail(item.id);
      } catch (error) {
        createError.textContent = error.message;
        createError.hidden = false;
        announce(`Work creation failed. ${error.message}`);
      } finally {
        submit.disabled = false;
      }
    });
    restoreCreateDraft();
    loadPersonas();
  }
  if (transitionDialog && transitionForm) {
    const closeTransition = () => transitionDialog.close();
    document.getElementById("work-transition-close").addEventListener("click", closeTransition);
    document.getElementById("work-transition-cancel").addEventListener("click", closeTransition);
    transitionDialog.addEventListener("close", () => {
      if (transitionCommand?.focus?.isConnected) transitionCommand.focus.focus();
      transitionCommand = undefined;
      transitionError.hidden = true;
    });
    transitionForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!transitionCommand) return;
      const command = transitionCommand;
      const submit = transitionForm.querySelector('button[type="submit"]');
      const reason = String(new FormData(transitionForm).get("reason") || "").trim();
      submit.disabled = true;
      transitionError.hidden = true;
      try {
        const updated = await mutateJSON(`${baseURL}/${encodeURIComponent(command.item.id)}/transitions`, "POST", { to: command.state, reason }, command.item.version);
        transitionDialog.close();
        renderDetail(updated, command.children);
        detail.focus();
        announce(`${command.text} succeeded for work item ${updated.number}.`);
        loadSummary();
        loadList(false);
      } catch (error) {
        const message = error.status === 412 ? "This item changed. Reloading the current version…" : error.message;
        transitionError.textContent = message;
        transitionError.hidden = false;
        announce(`Work state change failed. ${message}`);
        if (error.status === 412) {
          transitionDialog.close();
          window.setTimeout(() => loadDetail(command.item.id), 350);
        }
      } finally {
        submit.disabled = false;
      }
    });
  }
  if (assignmentDialog && assignmentForm) {
    const closeAssignment = () => assignmentDialog.close();
    document.getElementById("work-assignment-close").addEventListener("click", closeAssignment);
    document.getElementById("work-assignment-cancel").addEventListener("click", closeAssignment);
    assignmentForm.elements.responsibility.addEventListener("change", () => syncAssignmentFields(assignmentForm, "work-assignment"));
    assignmentDialog.addEventListener("close", () => {
      if (assignmentCommand?.focus?.isConnected) assignmentCommand.focus.focus();
      assignmentCommand = undefined;
      assignmentError.hidden = true;
    });
    assignmentForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!assignmentCommand) return;
      const command = assignmentCommand;
      const values = new FormData(assignmentForm);
      const submit = assignmentForm.querySelector('button[type="submit"]');
      submit.disabled = true;
      assignmentError.hidden = true;
      try {
        const updated = await mutateJSON(`${baseURL}/${encodeURIComponent(command.item.id)}/assignment`, "PATCH", { assignment: assignmentPayload(values), reason: String(values.get("reason") || "").trim() }, command.item.version);
        assignmentDialog.close();
        renderDetail(updated, command.children);
        detail.focus();
        announce(`Assignment updated for work item ${updated.number}.`);
        loadList(false);
      } catch (error) {
        const message = error.status === 412 ? "This item changed. Reloading the current version…" : error.message;
        assignmentError.textContent = message;
        assignmentError.hidden = false;
        announce(`Work assignment failed. ${message}`);
        if (error.status === 412) {
          assignmentDialog.close();
          window.setTimeout(() => loadDetail(command.item.id), 350);
        }
      } finally {
        submit.disabled = false;
      }
    });
  }
  loadSummary();
  loadList(false);
})();
