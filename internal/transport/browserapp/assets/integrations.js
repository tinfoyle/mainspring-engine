(() => {
  const root = document.getElementById("integrations-app");
  if (!root) return;
  const accountID = root.dataset.accountId;
  const actorUserID = root.dataset.userId;
  const readOnly = root.dataset.readOnly === "true";
  const base = `/api/v1/accounts/${encodeURIComponent(accountID)}/integrations`;
  const connectionList = document.getElementById("integrations-connection-list");
  const executionList = document.getElementById("integrations-execution-list");
  let connections = [], activeConnection = null, executions = [], activeExecution = null, editingConnection = null, rotatingCredential = false;

  const node = (tag, className, text) => { const value = document.createElement(tag); if (className) value.className = className; if (text !== undefined) value.textContent = text; return value; };
  const announce = (message) => { document.getElementById("integrations-command-status").textContent = message; };
  const status = (id, message) => { document.getElementById(id).textContent = message; };
  const date = (value) => value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "—";
  const shortID = (value) => value ? `${value.slice(0, 8)}…${value.slice(-4)}` : "—";
  const problem = async (response) => { let body = {}; try { body = await response.json(); } catch {} if (!response.ok) throw new Error(body.detail || body.title || "Integration request failed."); return { body, etag: response.headers.get("ETag") }; };
  const query = async (path) => problem(await fetch(path, { headers: { Accept: "application/json" } }));
  const command = async (method, path, body, version) => {
    const headers = { Accept: "application/json", "Idempotency-Key": crypto.randomUUID() };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (version) headers["If-Match"] = `W/"${version}"`;
    return problem(await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) }));
  };
  const empty = (target, message) => target.replaceChildren(node("p", "integrations-empty", message));
  const action = (label, handler, className = "") => { const button = node("button", className, label); button.type = "button"; button.addEventListener("click", handler); return button; };
  const setBusy = (busy) => root.setAttribute("aria-busy", String(busy));
  const detailList = (pairs) => { const list = node("dl"); for (const [term, value] of pairs) list.append(node("dt", "", term), node("dd", "", value || "—")); return list; };

  function connectionRow(value) {
    const button = node("button", "integrations-row"); button.type = "button";
    const copy = node("span"); copy.append(node("strong", "", value.name), node("small", "", `${value.kind === "web_publish" ? "Web publication" : "Email"} · revision ${value.current_revision}`));
    button.append(copy, node("em", `integrations-state integrations-state-${value.state}`, value.state.replace("_", " ")));
    button.addEventListener("click", () => selectConnection(value.id)); return button;
  }

  async function loadConnections(preferred) {
    status("integrations-connection-status", "Loading connections…");
    const state = document.getElementById("integrations-connection-state").value;
    try {
      const { body } = await query(`${base}/connections${state ? `?state=${encodeURIComponent(state)}` : ""}`);
      connections = body.items || []; connectionList.replaceChildren(...connections.map(connectionRow));
      status("integrations-connection-status", `${connections.length} connection${connections.length === 1 ? "" : "s"}.`);
      updateExecutionConnectionOptions();
      if (!connections.length) { empty(connectionList, "No connections match this view."); clearConnection(); }
      else await selectConnection(preferred || activeConnection?.connection.id || connections[0].id);
    } catch (error) { status("integrations-connection-status", error.message); empty(connectionList, "Connections could not be loaded."); }
  }

  function clearConnection() {
    activeConnection = null;
    document.getElementById("integrations-connection-detail").replaceChildren(node("p", "eyebrow", "CONNECTION DETAIL"), node("h2", "", "Select a connection"), node("p", "", "Inspect exact scope and current connector authority."));
  }

  async function selectConnection(id) {
    try { const { body } = await query(`${base}/connections/${id}`); activeConnection = body; renderConnection(); }
    catch (error) { announce(error.message); }
  }

  function scopeLines(detail) {
    const scope = detail.revision.scope || {};
    return [["Email address", scope.email_address], ["Audience reference", scope.audience_reference], ["HTTPS origin", scope.https_origin], ["Path prefix", scope.path_prefix]].filter((item) => item[1]);
  }

  function renderConnection() {
    const value = activeConnection.connection, revision = activeConnection.revision, target = document.getElementById("integrations-connection-detail");
    const health = activeConnection.latest_health;
    target.replaceChildren(node("p", "eyebrow", "CONNECTION DETAIL"), node("h2", "", value.name), node("p", "", "Only non-secret scope and credential-generation evidence are visible."),
      detailList([["State", value.state], ["Kind", value.kind], ["Capabilities", (revision.capabilities || []).join(" · ")], ["Revision", String(value.current_revision)], ["Credential generation", value.credential_generation ? String(value.credential_generation) : "Not bound"], ["Latest health", health ? `${health.state} · ${health.latency_milliseconds} ms · ${date(health.checked_at)}` : "No observation"], ...scopeLines(activeConnection)]));
    if (!readOnly && value.state !== "revoked") {
      const controls = node("div", "integrations-actions");
      controls.append(action("Revise scope", () => openConnection(activeConnection)));
      if (value.state === "pending") controls.append(action("Activate binding", () => openCredential(false)));
      if (value.state === "active") controls.append(action("Rotate binding", () => openCredential(true)), action("Disable", () => transitionConnection("disables"), "secondary"));
      if (value.state === "disabled") controls.append(action("Enable", () => transitionConnection("enables")), action("Rotate binding", () => openCredential(true)));
      controls.append(action("Revoke", () => transitionConnection("revocations"), "danger")); target.append(controls);
    }
  }

  async function transitionConnection(kind) {
    const value = activeConnection?.connection; if (!value) return;
    try { await command("POST", `${base}/connections/${value.id}/${kind}`, undefined, value.version); announce(`Connection ${kind === "disables" ? "disabled" : kind === "enables" ? "enabled" : "revoked"}.`); await loadConnections(value.id); }
    catch (error) { announce(error.message); }
  }

  function executionRow(value) {
    const button = node("button", "integrations-row"); button.type = "button";
    const copy = node("span"); copy.append(node("strong", "", `${value.capability} · release v${value.release_version}`), node("small", "", `${shortID(value.id)} · ${value.attempt_count} attempt${value.attempt_count === 1 ? "" : "s"} · ${date(value.updated_at)}`));
    button.append(copy, node("em", `integrations-state integrations-state-${value.state}`, value.state.replace("_", " ")));
    button.addEventListener("click", () => selectExecution(value.id)); return button;
  }

  async function loadExecutions(preferred) {
    status("integrations-execution-status", "Loading executions…");
    const state = document.getElementById("integrations-execution-state").value;
    try {
      const { body } = await query(`${base}/executions${state ? `?state=${encodeURIComponent(state)}` : ""}`);
      executions = body.items || []; executionList.replaceChildren(...executions.map(executionRow));
      status("integrations-execution-status", `${executions.length} execution${executions.length === 1 ? "" : "s"}.`);
      if (!executions.length) { empty(executionList, "No external executions match this view."); clearExecution(); }
      else await selectExecution(preferred || activeExecution?.execution.id || executions[0].id);
    } catch (error) { status("integrations-execution-status", error.message); empty(executionList, "Executions could not be loaded."); }
  }

  function clearExecution() {
    activeExecution = null;
    document.getElementById("integrations-execution-detail").replaceChildren(node("p", "eyebrow", "EXECUTION DETAIL"), node("h2", "", "Select an execution"), node("p", "", "Inspect frozen authority, attempts, and recovery evidence."));
  }

  async function selectExecution(id) {
    try { const { body } = await query(`${base}/executions/${id}`); activeExecution = body; renderExecution(); }
    catch (error) { announce(error.message); }
  }

  function renderExecution() {
    const value = activeExecution.execution, target = document.getElementById("integrations-execution-detail");
    target.replaceChildren(node("p", "eyebrow", "EXECUTION DETAIL"), node("h2", "", `${value.capability} · ${value.state.replace("_", " ")}`),
      detailList([["Execution", shortID(value.id)], ["Release", `${shortID(value.release_id)} · v${value.release_version}`], ["Connection", `${shortID(value.connection_id)} · revision ${value.connection_revision}`], ["Credential generation", String(value.credential_generation)], ["Payload SHA-256", value.payload_sha256], ["Last error", value.last_error_code], ["Completed", date(value.completed_at)]]));
    const attempts = node("section", "integrations-attempts"); attempts.append(node("h3", "", "Attempts"));
    for (const attempt of activeExecution.attempts || []) attempts.append(node("p", "", `#${attempt.number} ${attempt.mode} · ${attempt.outcome || "in flight"}${attempt.error_code ? ` · ${attempt.error_code}` : ""}`));
    if (!(activeExecution.attempts || []).length) attempts.append(node("p", "", "No provider attempt has started.")); target.append(attempts);
    const resolution = activeExecution.resolution;
    if (resolution) {
      const evidence = node("section", "integrations-resolution"); evidence.append(node("h3", "", "Manual resolution"),
        detailList([["Proposed outcome", resolution.requested_outcome], ["State", resolution.state], ["Evidence SHA-256", resolution.evidence_sha256], ["Requested", date(resolution.requested_at)], ["Confirmed", date(resolution.confirmed_at)]]));
      if (!readOnly && resolution.state === "pending") {
        if (resolution.requested_by_user_id === actorUserID) evidence.append(node("p", "integrations-warning", "A different Owner or Administrator must confirm this outcome."));
        else evidence.append(action("Confirm exact outcome", () => confirmResolution(value.id, resolution.id), "danger"));
      }
      target.append(evidence);
    } else if (!readOnly && value.state === "manual_resolution") {
      target.append(action("Propose resolution", () => openResolution(value.id), "danger"));
    }
  }

  async function confirmResolution(executionID, resolutionID) {
    try { const { body } = await command("POST", `${base}/executions/${executionID}/resolutions/${resolutionID}/confirmations`); activeExecution = body; announce("External outcome confirmed by dual control."); renderExecution(); await loadExecutions(executionID); }
    catch (error) { announce(error.message); }
  }

  const openDialog = (dialog, form) => { const error = form.querySelector(".integrations-error"); error.hidden = true; error.textContent = ""; dialog.showModal(); form.elements[0]?.focus(); };
  const values = (form) => Object.fromEntries(new FormData(form));
  const showFormError = (form, error) => { const target = form.querySelector(".integrations-error"); target.textContent = error.message; target.hidden = false; };
  const selectedCapabilities = (form) => [...form.elements.capabilities.selectedOptions].map((option) => option.value);
  const scopeBody = (data) => ({ email_address: data.email_address || "", audience_reference: data.audience_reference || "", https_origin: data.https_origin || "", path_prefix: data.path_prefix || "" });

  function openConnection(detail) {
    editingConnection = detail || null; const form = document.getElementById("integrations-connection-form"); form.reset();
    document.getElementById("integrations-connection-form-title").textContent = detail ? "Revise connection" : "New connection";
    form.elements.kind.disabled = Boolean(detail);
    if (detail) { const value = detail.connection, revision = detail.revision, scope = revision.scope || {}; form.elements.name.value = value.name; form.elements.kind.value = value.kind; for (const option of form.elements.capabilities.options) option.selected = revision.capabilities.includes(option.value); for (const field of ["email_address", "audience_reference", "https_origin", "path_prefix"]) form.elements[field].value = scope[field] || ""; }
    openDialog(document.getElementById("integrations-connection-dialog"), form);
  }

  function openCredential(rotate) {
    rotatingCredential = rotate; const form = document.getElementById("integrations-credential-form"); form.reset();
    form.elements.expected_generation.value = rotate ? activeConnection.connection.credential_generation : 0;
    document.getElementById("integrations-credential-title").textContent = rotate ? "Rotate binding" : "Activate binding";
    openDialog(document.getElementById("integrations-credential-dialog"), form);
  }

  function updateExecutionConnectionOptions() {
    const select = document.querySelector('#integrations-execution-form [name="connection_id"]'); if (!select) return;
    const values = connections.filter((item) => item.state === "active"); select.replaceChildren(...values.map((item) => { const option = node("option", "", item.name); option.value = item.id; return option; }));
  }

  function openExecution() { const form = document.getElementById("integrations-execution-form"); form.reset(); updateExecutionConnectionOptions(); openDialog(document.getElementById("integrations-execution-dialog"), form); }
  function openResolution(executionID) { const form = document.getElementById("integrations-resolution-form"); form.reset(); form.elements.execution_id.value = executionID; openDialog(document.getElementById("integrations-resolution-dialog"), form); }

  document.querySelectorAll("[data-close-integrations]").forEach((button) => button.addEventListener("click", () => button.closest("dialog").close()));
  document.getElementById("integrations-refresh").addEventListener("click", async () => { setBusy(true); await Promise.all([loadConnections(), loadExecutions()]); setBusy(false); });
  document.getElementById("integrations-connection-state").addEventListener("change", () => loadConnections());
  document.getElementById("integrations-execution-state").addEventListener("change", () => loadExecutions());
  document.getElementById("integrations-new-connection")?.addEventListener("click", () => openConnection());
  document.getElementById("integrations-prepare-execution")?.addEventListener("click", openExecution);

  document.getElementById("integrations-connection-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget, data = values(form); const body = { name: data.name, capabilities: selectedCapabilities(form), scope: scopeBody(data) }; if (!editingConnection) body.kind = data.kind; try { const value = editingConnection?.connection; const { body: result } = await command(editingConnection ? "PUT" : "POST", editingConnection ? `${base}/connections/${value.id}` : `${base}/connections`, body, value?.version); form.closest("dialog").close(); announce(editingConnection ? "Connection scope revised." : "Connection created pending credential binding."); await loadConnections(result.id); } catch (error) { showFormError(form, error); } });
  document.getElementById("integrations-credential-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget, data = values(form), value = activeConnection.connection; const body = { provider: data.provider, reference_sha256: data.reference_sha256 }; if (rotatingCredential) body.expected_generation = Number(data.expected_generation); if (data.expires_at) body.expires_at = new Date(data.expires_at).toISOString(); try { await command("POST", `${base}/connections/${value.id}/${rotatingCredential ? "credential-rotations" : "credential-bindings"}`, body, value.version); form.closest("dialog").close(); announce(rotatingCredential ? "Credential binding rotated." : "Connection activated."); await loadConnections(value.id); } catch (error) { showFormError(form, error); } });
  document.getElementById("integrations-execution-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget, data = values(form); try { const { body } = await command("POST", `${base}/executions`, { release_id: data.release_id, release_version: Number(data.release_version), capability: data.capability, connection_id: data.connection_id }); form.closest("dialog").close(); announce("External execution prepared with frozen authority."); await loadExecutions(body.id); } catch (error) { showFormError(form, error); } });
  document.getElementById("integrations-resolution-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget, data = values(form); try { const { body } = await command("POST", `${base}/executions/${data.execution_id}/resolution-requests`, { requested_outcome: data.requested_outcome, evidence: data.evidence }); form.closest("dialog").close(); activeExecution = body; announce("Resolution requested. A different manager must confirm it."); renderExecution(); await loadExecutions(data.execution_id); } catch (error) { showFormError(form, error); } });

  setBusy(true); Promise.all([loadConnections(), loadExecutions()]).finally(() => setBusy(false));
})();
