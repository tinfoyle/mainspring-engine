(() => {
  const root = document.getElementById("marketing-app");
  if (!root) return;
  const accountID = root.dataset.accountId;
  const readOnly = root.dataset.readOnly === "true";
  const base = `/api/v1/accounts/${encodeURIComponent(accountID)}/marketing`;
  const campaignList = document.getElementById("marketing-campaign-list");
  const assetList = document.getElementById("marketing-asset-list");
  const releaseList = document.getElementById("marketing-release-list");
  let campaigns = [], activeCampaign = null, releases = [], editingCampaign = null;

  const node = (tag, className, text) => { const value = document.createElement(tag); if (className) value.className = className; if (text !== undefined) value.textContent = text; return value; };
  const announce = (message) => { document.getElementById("marketing-command-status").textContent = message; };
  const channels = (value) => (value || []).map((item) => item === "email" ? "Email" : "Web").join(" · ");
  const date = (value) => new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value));
  const problem = async (response) => { let body = {}; try { body = await response.json(); } catch {} if (!response.ok) throw new Error(body.detail || body.title || "Marketing request failed."); return { body, etag: response.headers.get("ETag") }; };
  const query = async (path) => problem(await fetch(path, { headers: { Accept: "application/json" } }));
  const command = async (method, path, body, version) => {
    const headers = { Accept: "application/json", "Idempotency-Key": crypto.randomUUID() };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (version) headers["If-Match"] = `W/"${version}"`;
    return problem(await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) }));
  };
  const empty = (target, message) => target.replaceChildren(node("p", "marketing-empty", message));
  const status = (id, message) => { document.getElementById(id).textContent = message; };
  const setBusy = (busy) => root.setAttribute("aria-busy", String(busy));

  function campaignRow(value) {
    const button = node("button", "marketing-row"); button.type = "button";
    const copy = node("span"); copy.append(node("strong", "", value.name), node("small", "", `${channels(value.channels)} · Updated ${date(value.updated_at)}`));
    const state = node("em", `marketing-state marketing-state-${value.state}`, value.state); button.append(copy, state);
    button.addEventListener("click", () => selectCampaign(value.id)); return button;
  }

  async function loadCampaigns(preferred) {
    setBusy(true); status("marketing-campaign-status", "Loading campaigns…");
    try {
      const state = document.getElementById("marketing-state").value;
      const { body } = await query(`${base}/campaigns${state ? `?state=${encodeURIComponent(state)}` : ""}`);
      campaigns = body.items || []; campaignList.replaceChildren(...campaigns.map(campaignRow));
      status("marketing-campaign-status", `${campaigns.length} campaign${campaigns.length === 1 ? "" : "s"}.`);
      if (!campaigns.length) { empty(campaignList, "No campaigns match this view."); clearCampaign(); }
      else await selectCampaign(preferred || activeCampaign?.id || campaigns[0].id);
    } catch (error) { status("marketing-campaign-status", error.message); empty(campaignList, "Campaigns could not be loaded."); }
    finally { setBusy(false); }
  }

  function clearCampaign() {
    activeCampaign = null; releases = []; document.getElementById("marketing-campaign-detail").replaceChildren(node("p", "eyebrow", "CAMPAIGN DETAIL"), node("h2", "", "Select a campaign"), node("p", "", "Inspect its exact intent and governed release snapshots."));
    empty(assetList, "Select a campaign."); empty(releaseList, "Select a campaign.");
    for (const id of ["marketing-new-asset", "marketing-new-release"]) { const control = document.getElementById(id); if (control) control.disabled = true; }
  }

  async function selectCampaign(id) {
    try {
      const [{ body: campaign }, { body: assets }, { body: releasePage }] = await Promise.all([query(`${base}/campaigns/${id}`), query(`${base}/campaigns/${id}/asset-revisions`), query(`${base}/campaigns/${id}/releases`)]);
      activeCampaign = campaign; releases = releasePage.items || []; renderCampaign(); renderAssets(assets.items || []); renderReleases();
      for (const controlID of ["marketing-new-asset", "marketing-new-release"]) { const control = document.getElementById(controlID); if (control) control.disabled = false; }
    } catch (error) { announce(error.message); }
  }

  function action(label, handler, kind = "") { const button = node("button", kind, label); button.type = "button"; button.addEventListener("click", handler); return button; }
  async function runCampaignTransition(kind, releaseID) {
    if (!activeCampaign) return;
    const path = kind === "archive" ? `${base}/campaigns/${activeCampaign.id}` : `${base}/campaigns/${activeCampaign.id}/${kind === "activate" ? "activations" : kind === "pause" ? "pauses" : "completions"}`;
    try { await command(kind === "archive" ? "DELETE" : "POST", path, kind === "activate" ? { release_id: releaseID } : undefined, activeCampaign.version); announce(`Campaign ${kind}d.`); await loadCampaigns(activeCampaign.id); } catch (error) { announce(error.message); }
  }

  function renderCampaign() {
    const target = document.getElementById("marketing-campaign-detail"); const title = node("h2", "", activeCampaign.name); const summary = node("p", "", activeCampaign.objective); const audience = node("p", "", `Audience: ${activeCampaign.audience}`); const meta = node("dl");
    for (const [term, value] of [["State", activeCampaign.state], ["Channels", channels(activeCampaign.channels)], ["Version", String(activeCampaign.version)], ["Updated", date(activeCampaign.updated_at)]]) { meta.append(node("dt", "", term), node("dd", "", value)); }
    target.replaceChildren(node("p", "eyebrow", "CAMPAIGN DETAIL"), title, summary, audience, meta);
    if (!readOnly) {
      const controls = node("div", "marketing-actions");
      if (["draft", "paused"].includes(activeCampaign.state)) controls.append(action("Revise", () => openCampaign(activeCampaign)));
      if (activeCampaign.state === "active") controls.append(action("Pause", () => runCampaignTransition("pause")));
      if (["active", "paused"].includes(activeCampaign.state)) controls.append(action("Complete", () => runCampaignTransition("complete")));
      if (!["active", "archived"].includes(activeCampaign.state)) controls.append(action("Archive", () => runCampaignTransition("archive"), "danger"));
      target.append(controls);
    }
  }

  function renderAssets(items) {
    status("marketing-asset-status", `${items.length} immutable revision${items.length === 1 ? "" : "s"}.`);
    if (!items.length) return empty(assetList, "No creative revisions yet.");
    assetList.replaceChildren(...items.map((item) => { const row = node("article", "marketing-record"); row.append(node("strong", "", item.title), node("small", "", `${item.kind} · revision ${item.revision} · ${item.media_type}`), node("code", "", item.content_sha256.slice(0, 16) + "…")); return row; }));
  }

  async function releaseTransition(value, kind) {
    let body; if (kind === "submit") body = { campaign_version: activeCampaign.version };
    if (kind === "approve") { const approval = window.prompt("Approved Attention decision ID"); if (!approval) return; body = { approval_id: approval }; }
    try { await command("POST", `${base}/releases/${value.id}/${kind === "submit" ? "submissions" : kind === "approve" ? "approvals" : "cancellations"}`, body, value.version); announce(`Release ${kind}d.`); await selectCampaign(activeCampaign.id); } catch (error) { announce(error.message); }
  }

  function renderReleases() {
    status("marketing-release-status", `${releases.length} release snapshot${releases.length === 1 ? "" : "s"}.`);
    if (!releases.length) return empty(releaseList, "No release snapshots yet.");
    releaseList.replaceChildren(...releases.map((value) => { const row = node("article", "marketing-record"); row.append(node("strong", "", value.name), node("small", "", `${value.state} · campaign v${value.campaign_version} · ${value.asset_revision_ids.length} asset${value.asset_revision_ids.length === 1 ? "" : "s"}`));
      if (!readOnly) { const controls = node("div", "marketing-record-actions"); if (value.state === "draft") controls.append(action("Submit", () => releaseTransition(value, "submit"))); if (value.state === "submitted") controls.append(action("Approve", () => releaseTransition(value, "approve"))); if (["submitted", "approved"].includes(value.state)) controls.append(action("Cancel", () => releaseTransition(value, "cancel"), "danger")); if (value.state === "approved" && ["draft", "paused"].includes(activeCampaign.state)) controls.append(action("Activate", () => runCampaignTransition("activate", value.id))); row.append(controls); } return row; }));
  }

  const openDialog = (dialog, form) => { form.querySelector(".marketing-error").hidden = true; dialog.showModal(); form.elements[0]?.focus(); };
  function openCampaign(value) { editingCampaign = value || null; const form = document.getElementById("marketing-campaign-form"); form.reset(); document.getElementById("marketing-campaign-form-title").textContent = value ? "Revise campaign" : "New campaign"; if (value) { form.elements.name.value = value.name; form.elements.objective.value = value.objective; form.elements.audience.value = value.audience; for (const input of form.querySelectorAll('[name="channel"]')) input.checked = value.channels.includes(input.value); } openDialog(document.getElementById("marketing-campaign-dialog"), form); }
  const selectedChannels = (form) => [...form.querySelectorAll('[name="channel"]:checked')].map((item) => item.value);
  const values = (form) => Object.fromEntries(new FormData(form));
  const showFormError = (form, error) => { const target = form.querySelector(".marketing-error"); target.textContent = error.message; target.hidden = false; };

  document.querySelectorAll("[data-close-marketing]").forEach((button) => button.addEventListener("click", () => button.closest("dialog").close()));
  document.getElementById("marketing-refresh").addEventListener("click", () => loadCampaigns(activeCampaign?.id));
  document.getElementById("marketing-state").addEventListener("change", () => loadCampaigns());
  document.getElementById("marketing-new-campaign")?.addEventListener("click", () => openCampaign());
  document.getElementById("marketing-new-asset")?.addEventListener("click", () => { const form = document.getElementById("marketing-asset-form"); form.reset(); form.elements.asset_id.value = crypto.randomUUID(); openDialog(document.getElementById("marketing-asset-dialog"), form); });
  document.getElementById("marketing-new-release")?.addEventListener("click", () => { const form = document.getElementById("marketing-release-form"); form.reset(); for (const input of form.querySelectorAll('[name="channel"]')) input.checked = activeCampaign.channels.includes(input.value); openDialog(document.getElementById("marketing-release-dialog"), form); });

  document.getElementById("marketing-campaign-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget; const data = values(form); const body = { name: data.name, objective: data.objective, audience: data.audience, channels: selectedChannels(form) }; try { const path = editingCampaign ? `${base}/campaigns/${editingCampaign.id}` : `${base}/campaigns`; const result = await command(editingCampaign ? "PUT" : "POST", path, body, editingCampaign?.version); form.closest("dialog").close(); announce(editingCampaign ? "Campaign revised." : "Campaign created."); await loadCampaigns(result.body.id); } catch (error) { showFormError(form, error); } });
  document.getElementById("marketing-asset-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget; const data = values(form); try { await command("POST", `${base}/campaigns/${activeCampaign.id}/asset-revisions`, { asset_id: data.asset_id, kind: data.kind, title: data.title, media_type: data.media_type, content_reference: data.content_reference, content_sha256: data.content_sha256, content_bytes: Number(data.content_bytes), alternative_text: data.alternative_text }); form.closest("dialog").close(); announce("Immutable asset revision added."); await selectCampaign(activeCampaign.id); } catch (error) { showFormError(form, error); } });
  document.getElementById("marketing-release-form")?.addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget; const data = values(form); const ids = data.asset_revision_ids.split(",").map((item) => item.trim()).filter(Boolean); try { await command("POST", `${base}/campaigns/${activeCampaign.id}/releases`, { campaign_version: activeCampaign.version, name: data.name, channels: selectedChannels(form), asset_revision_ids: ids }); form.closest("dialog").close(); announce("Release snapshot created."); await selectCampaign(activeCampaign.id); } catch (error) { showFormError(form, error); } });
  loadCampaigns();
})();
