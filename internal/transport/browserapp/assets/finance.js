(() => {
  "use strict";
  const root = document.getElementById("finance-app");
  if (!root) return;

  const accountID = root.dataset.accountId;
  const readOnly = root.dataset.readOnly === "true";
  const base = `/api/v1/accounts/${encodeURIComponent(accountID)}/finance`;
  const ledgerSelect = document.getElementById("finance-ledger");
  const commandStatus = document.getElementById("finance-command-status");
  const accountsNode = document.getElementById("finance-accounts");
  const entriesNode = document.getElementById("finance-entries");
  const reconciliationsNode = document.getElementById("finance-reconciliations");
  const accountDetail = document.getElementById("finance-account-detail");
  const entryDetail = document.getElementById("finance-entry-detail");
  const reconciliationDetail = document.getElementById("finance-reconciliation-detail");
  let ledgers = [];
  let accounts = [];
  let activeLedger = null;
  let activeLedgerETag = "";

  const node = (tag, className, value) => {
    const item = document.createElement(tag);
    if (className) item.className = className;
    if (value !== undefined) item.textContent = value;
    return item;
  };
  const label = (value) => String(value || "unknown").replaceAll("_", " ");
  const dateLabel = (value) => value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeZone: "UTC" }).format(new Date(value)) : "—";
  const dateISO = (value) => new Date(`${value}T00:00:00.000Z`).toISOString();
  const toMinor = (value) => Math.round(Number(value) * 100);
  const evidence = (value) => String(value || "").split(",").map((item) => item.trim()).filter(Boolean);
  const money = (minor, currency = activeLedger?.currency || "USD") => {
    try { return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(Number(minor || 0) / 100); }
    catch (_) { return `${currency} ${(Number(minor || 0) / 100).toFixed(2)}`; }
  };
  const announce = (message) => {
    commandStatus.textContent = "";
    window.setTimeout(() => { commandStatus.textContent = message; }, 0);
  };

  async function request(path, options = {}) {
    const response = await fetch(path, {
      ...options,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(options.headers || {}) },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.detail || body.title || "Finance request failed.");
    return { body, etag: response.headers.get("ETag") || "" };
  }

  function mutationHeaders(etag = "", json = true) {
    return {
      ...(json ? { "Content-Type": "application/json" } : {}),
      "Idempotency-Key": crypto.randomUUID(),
      ...(etag ? { "If-Match": etag } : {}),
    };
  }

  function setSummary(ledger) {
    document.getElementById("finance-income").textContent = ledger ? money(ledger.income_minor, ledger.currency) : "—";
    document.getElementById("finance-expense").textContent = ledger ? money(ledger.expense_minor, ledger.currency) : "—";
    document.getElementById("finance-net").textContent = ledger ? money(ledger.net_minor, ledger.currency) : "—";
    document.getElementById("finance-drafts").textContent = ledger ? String(ledger.draft_count) : "—";
  }

  function empty(target, message) {
    target.replaceChildren(node("p", "finance-empty", message));
  }

  function row(primary, secondary, amount, state, onClick) {
    const button = node("button", "finance-row");
    button.type = "button";
    const content = node("span");
    content.append(node("strong", "", primary), node("small", "", secondary));
    const meta = node("span");
    meta.append(node("b", "", amount), node("em", "", label(state)));
    button.append(content, meta);
    button.addEventListener("click", onClick);
    return button;
  }

  function details(items) {
    const list = node("dl");
    for (const [term, value] of items) list.append(node("dt", "", term), node("dd", "", value));
    return list;
  }

  function actionButton(text, className, action) {
    const button = node("button", className, text);
    button.type = "button";
    button.addEventListener("click", async () => {
      button.disabled = true;
      try { await action(); }
      catch (error) { announce(error.message); button.disabled = false; }
    });
    return button;
  }

  async function loadLedgers(preferredID = ledgerSelect.value) {
    root.setAttribute("aria-busy", "true");
    try {
      const { body } = await request(`${base}/ledgers?limit=100`);
      ledgers = body.items;
      ledgerSelect.replaceChildren();
      if (!ledgers.length) {
        ledgerSelect.append(new Option("No ledgers yet", ""));
        ledgerSelect.disabled = true;
        activeLedger = null;
        setSummary(null);
        await clearWorkspace("Create a ledger to begin governed Finance work.");
        return;
      }
      for (const ledger of ledgers) ledgerSelect.append(new Option(`${ledger.code} · ${ledger.name}`, ledger.id));
      ledgerSelect.disabled = false;
      ledgerSelect.value = ledgers.some((item) => item.id === preferredID) ? preferredID : ledgers[0].id;
      await selectLedger();
    } catch (error) {
      ledgerSelect.replaceChildren(new Option("Finance unavailable", ""));
      await clearWorkspace(error.message);
      announce(error.message);
    } finally { root.setAttribute("aria-busy", "false"); }
  }

  async function selectLedger() {
    activeLedger = ledgers.find((ledger) => ledger.id === ledgerSelect.value) || null;
    setSummary(activeLedger);
    const manage = document.getElementById("finance-manage-ledger");
    if (manage) manage.disabled = !activeLedger || activeLedger.state === "archived";
    if (!activeLedger) return clearWorkspace("Choose a ledger.");
    const ledgerPath = `${base}/ledgers/${encodeURIComponent(activeLedger.id)}`;
    const detail = await request(ledgerPath);
    activeLedgerETag = detail.etag;
    activeLedger = { ...activeLedger, ...detail.body };
    await Promise.all([loadAccounts(), loadEntries(), loadReconciliations()]);
  }

  async function clearWorkspace(message) {
    accounts = [];
    empty(accountsNode, message);
    empty(entriesNode, message);
    empty(reconciliationsNode, message);
    for (const id of ["finance-accounts-status", "finance-entries-status", "finance-reconciliations-status"]) document.getElementById(id).textContent = message;
  }

  async function loadAccounts() {
    const status = document.getElementById("finance-accounts-status");
    status.textContent = "Loading accounts…";
    try {
      const { body } = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}/accounts?limit=100`);
      accounts = body.items;
      status.textContent = `${accounts.length} account${accounts.length === 1 ? "" : "s"}`;
      if (!accounts.length) empty(accountsNode, "No chart accounts have been added.");
      else accountsNode.replaceChildren(...accounts.map((item) => row(`${item.account.code} · ${item.account.name}`, `${label(item.account.type)} · ${label(item.account.normal_balance)} normal`, money(item.balance_minor), item.account.state, () => loadAccount(item.account.id))));
      populatePostingAccounts();
    } catch (error) { status.textContent = error.message; empty(accountsNode, "Accounts could not be loaded."); }
  }

  async function loadEntries() {
    const status = document.getElementById("finance-entries-status");
    const state = document.getElementById("finance-entry-state").value;
    status.textContent = "Loading entries…";
    try {
      const query = new URLSearchParams({ limit: "100" });
      if (state) query.set("state", state);
      const { body } = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}/entries?${query}`);
      status.textContent = `${body.items.length} entr${body.items.length === 1 ? "y" : "ies"}`;
      if (!body.items.length) empty(entriesNode, "No journal entries match this view.");
      else entriesNode.replaceChildren(...body.items.map((item) => row(`#${item.number} · ${item.description}`, `${dateLabel(item.entry_date)} · ${item.reference || "No reference"}`, money(item.total_minor, item.currency), item.state, () => loadEntry(item.id))));
    } catch (error) { status.textContent = error.message; empty(entriesNode, "Entries could not be loaded."); }
  }

  async function loadReconciliations() {
    const status = document.getElementById("finance-reconciliations-status");
    status.textContent = "Loading reconciliations…";
    try {
      const { body } = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}/reconciliations?limit=100`);
      status.textContent = `${body.items.length} reconciliation${body.items.length === 1 ? "" : "s"}`;
      if (!body.items.length) empty(reconciliationsNode, "No statement checks have been recorded.");
      else reconciliationsNode.replaceChildren(...body.items.map((item) => row(dateLabel(item.as_of), accountName(item.posting_account_id), money(item.difference_minor, item.statement_balance.currency), item.state, () => loadReconciliation(item.id))));
    } catch (error) { status.textContent = error.message; empty(reconciliationsNode, "Reconciliations could not be loaded."); }
  }

  function accountName(id) {
    const match = accounts.find((item) => item.account.id === id);
    return match ? `${match.account.code} · ${match.account.name}` : "Posting account";
  }

  function populatePostingAccounts() {
    const candidates = accounts.filter((item) => item.account.allow_posting && item.account.state === "active");
    for (const select of document.querySelectorAll('#finance-entry-form select[name$="_account"], #finance-reconciliation-form select[name="posting_account_id"]')) {
      const selected = select.value;
      select.replaceChildren(new Option("Choose an account", ""), ...candidates.map((item) => new Option(`${item.account.code} · ${item.account.name}`, item.account.id)));
      if (candidates.some((item) => item.account.id === selected)) select.value = selected;
    }
  }

  async function loadAccount(id) {
    accountDetail.replaceChildren(node("p", "eyebrow", "ACCOUNT DETAIL"), node("h2", "", "Loading account…"));
    try {
      const { body, etag } = await request(`${base}/accounts/${encodeURIComponent(id)}`);
      const summary = accounts.find((item) => item.account.id === id);
      const content = [node("p", "eyebrow", "ACCOUNT DETAIL"), node("h2", "", `${body.code} · ${body.name}`), node("p", "", body.description || "No description."), details([["Type", label(body.type)], ["Normal balance", label(body.normal_balance)], ["Current balance", money(summary?.balance_minor || 0)], ["Posting", body.allow_posting ? "Allowed" : "Summary only"], ["State", label(body.state)], ["Version", String(body.version)]])];
      if (!readOnly && body.state === "active") {
        const actions = node("div", "finance-detail-actions");
        actions.append(actionButton("Edit account", "secondary", () => openAccountDialog(body, etag)), actionButton("Archive account", "danger", async () => {
          if (!window.confirm(`Archive ${body.code} · ${body.name}?`)) return;
          await request(`${base}/accounts/${encodeURIComponent(body.id)}`, { method: "DELETE", headers: mutationHeaders(etag, false) });
          announce("Posting account archived."); await loadAccounts(); accountDetail.replaceChildren(node("p", "eyebrow", "ACCOUNT DETAIL"), node("h2", "", "Account archived"));
        }));
        content.push(actions);
      }
      accountDetail.replaceChildren(...content); accountDetail.focus();
    } catch (error) { accountDetail.replaceChildren(node("p", "eyebrow", "ACCOUNT DETAIL"), node("h2", "", "Account unavailable"), node("p", "", error.message)); }
  }

  async function loadEntry(id) {
    entryDetail.replaceChildren(node("p", "eyebrow", "ENTRY DETAIL"), node("h2", "", "Loading entry…"));
    try {
      const { body, etag } = await request(`${base}/entries/${encodeURIComponent(id)}`);
      const table = node("table", "finance-lines");
      const head = node("thead"); const headings = node("tr");
      for (const value of ["Account", "Memo", "Debit", "Credit"]) headings.append(node("th", "", value));
      head.append(headings); const rows = node("tbody");
      for (const line of body.lines) {
        const item = node("tr");
        for (const value of [accountName(line.account_id), line.memo || "—", line.debit_minor ? money(line.debit_minor, body.currency) : "—", line.credit_minor ? money(line.credit_minor, body.currency) : "—"]) item.append(node("td", "", value));
        rows.append(item);
      }
      table.append(head, rows);
      const content = [node("p", "eyebrow", "ENTRY DETAIL"), node("h2", "", `#${body.number} · ${body.description}`), node("p", "", `${dateLabel(body.entry_date)} · ${body.reference || "No reference"}`), details([["State", label(body.state)], ["Source", label(body.provenance.source)], ["Total", money(body.total_minor, body.currency)], ["Evidence", String(body.evidence?.length || 0)], ["Version", String(body.version)]]), table];
      if (!readOnly && body.state === "draft") {
        const actions = node("div", "finance-detail-actions");
        actions.append(actionButton("Post entry", "", async () => {
          if (!window.confirm("Post this balanced draft? Posted entries are immutable.")) return;
          await request(`${base}/entries/${encodeURIComponent(body.id)}/postings`, { method: "POST", headers: mutationHeaders(etag, false) });
          announce("Journal entry posted."); await Promise.all([loadLedgers(activeLedger.id), loadEntry(body.id)]);
        }));
        content.push(actions);
      } else if (!readOnly && body.state === "posted") {
        const actions = node("div", "finance-detail-actions");
        actions.append(actionButton("Reverse entry", "danger", async () => {
          if (!window.confirm("Create a separately numbered reversal for this posting?")) return;
          await request(`${base}/entries/${encodeURIComponent(body.id)}/reversals`, { method: "POST", headers: mutationHeaders(etag), body: JSON.stringify({ entry_date: new Date().toISOString(), description: `Reversal of #${body.number}: ${body.description}`, reference: `REV-${body.reference || body.number}`, evidence: body.evidence || [] }) });
          announce("Reversal posted and original entry marked reversed."); await loadLedgers(activeLedger.id);
        }));
        content.push(actions);
      }
      entryDetail.replaceChildren(...content); entryDetail.focus();
    } catch (error) { entryDetail.replaceChildren(node("p", "eyebrow", "ENTRY DETAIL"), node("h2", "", "Entry unavailable"), node("p", "", error.message)); }
  }

  async function loadReconciliation(id) {
    reconciliationDetail.replaceChildren(node("p", "eyebrow", "RECONCILIATION DETAIL"), node("h2", "", "Loading reconciliation…"));
    try {
      const { body, etag } = await request(`${base}/reconciliations/${encodeURIComponent(id)}`);
      const content = [node("p", "eyebrow", "RECONCILIATION DETAIL"), node("h2", "", accountName(body.posting_account_id)), node("p", "", `Statement position as of ${dateLabel(body.as_of)}.`), details([["Statement", money(body.statement_balance.minor, body.statement_balance.currency)], ["Ledger", money(body.ledger_balance.minor, body.ledger_balance.currency)], ["Difference", money(body.difference_minor, body.statement_balance.currency)], ["Evidence", String(body.evidence.length)], ["State", label(body.state)], ["Version", String(body.version)]])];
      if (!readOnly && body.state === "proposed" && body.difference_minor === 0) {
        const actions = node("div", "finance-detail-actions");
        actions.append(actionButton("Confirm reconciliation", "", async () => {
          if (!window.confirm("Confirm this zero-difference reconciliation?")) return;
          await request(`${base}/reconciliations/${encodeURIComponent(body.id)}/confirmations`, { method: "POST", headers: mutationHeaders(etag, false) });
          announce("Reconciliation confirmed."); await Promise.all([loadReconciliations(), loadReconciliation(body.id)]);
        }));
        content.push(actions);
      }
      reconciliationDetail.replaceChildren(...content); reconciliationDetail.focus();
    } catch (error) { reconciliationDetail.replaceChildren(node("p", "eyebrow", "RECONCILIATION DETAIL"), node("h2", "", "Reconciliation unavailable"), node("p", "", error.message)); }
  }

  function formError(form, message = "") {
    const target = form.querySelector(".finance-form-error");
    target.textContent = message; target.hidden = !message;
  }

  function openDialog(dialog, form) {
    formError(form); dialog.showModal(); form.querySelector("input, select, textarea")?.focus();
  }

  async function openLedgerDialog(edit = false) {
    const dialog = document.getElementById("finance-ledger-dialog"); const form = document.getElementById("finance-ledger-form");
    form.reset(); form.dataset.mode = edit ? "edit" : "create";
    document.getElementById("finance-ledger-title").textContent = edit ? "Manage ledger" : "Establish a book";
    document.getElementById("finance-save-ledger").textContent = edit ? "Save changes" : "Create ledger";
    document.getElementById("finance-ledger-governance").hidden = !edit;
    form.elements.currency.disabled = edit;
    if (edit) {
      const result = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}`);
      activeLedger = { ...activeLedger, ...result.body }; activeLedgerETag = result.etag;
      form.elements.name.value = activeLedger.name; form.elements.code.value = activeLedger.code; form.elements.currency.value = activeLedger.currency; form.elements.description.value = activeLedger.description;
    } else form.elements.currency.value = "USD";
    openDialog(dialog, form);
  }

  function openAccountDialog(value = null, etag = "") {
    const dialog = document.getElementById("finance-account-dialog"); const form = document.getElementById("finance-account-form");
    form.reset(); form.dataset.accountId = value?.id || ""; form.dataset.etag = etag;
    document.getElementById("finance-account-title").textContent = value ? "Edit posting account" : "Add posting account";
    form.querySelector('button[type="submit"]').textContent = value ? "Save changes" : "Add account";
    form.elements.type.disabled = Boolean(value);
    if (value) { form.elements.code.value = value.code; form.elements.name.value = value.name; form.elements.description.value = value.description; form.elements.type.value = value.type; form.elements.allow_posting.checked = value.allow_posting; }
    openDialog(dialog, form);
  }

  for (const button of document.querySelectorAll("[data-close-dialog]")) button.addEventListener("click", () => button.closest("dialog").close());
  for (const tab of document.querySelectorAll("[data-finance-tab]")) tab.addEventListener("click", () => {
    for (const candidate of document.querySelectorAll("[data-finance-tab]")) candidate.setAttribute("aria-selected", String(candidate === tab));
    for (const panel of document.querySelectorAll(".finance-tab-panel")) panel.hidden = panel.id !== `finance-${tab.dataset.financeTab}-panel`;
  });

  ledgerSelect.addEventListener("change", selectLedger);
  document.getElementById("finance-entry-state").addEventListener("change", loadEntries);
  document.getElementById("finance-refresh").addEventListener("click", () => loadLedgers(activeLedger?.id));
  document.getElementById("finance-new-ledger")?.addEventListener("click", () => openLedgerDialog(false));
  document.getElementById("finance-manage-ledger")?.addEventListener("click", () => openLedgerDialog(true).catch((error) => announce(error.message)));
  document.getElementById("finance-new-account")?.addEventListener("click", () => openAccountDialog());
  document.getElementById("finance-new-entry")?.addEventListener("click", () => {
    const form = document.getElementById("finance-entry-form"); form.reset(); populatePostingAccounts(); form.elements.entry_date.value = new Date().toISOString().slice(0, 10); openDialog(document.getElementById("finance-entry-dialog"), form);
  });
  document.getElementById("finance-new-reconciliation")?.addEventListener("click", () => {
    const form = document.getElementById("finance-reconciliation-form"); form.reset(); populatePostingAccounts(); form.elements.as_of.value = new Date().toISOString().slice(0, 10); openDialog(document.getElementById("finance-reconciliation-dialog"), form);
  });

  document.getElementById("finance-ledger-form")?.addEventListener("submit", async (event) => {
    event.preventDefault(); const form = event.currentTarget; const submit = event.submitter; submit.disabled = true; formError(form);
    try {
      const edit = form.dataset.mode === "edit";
      const body = { name: form.elements.name.value, code: form.elements.code.value, description: form.elements.description.value };
      if (!edit) body.currency = form.elements.currency.value.toUpperCase();
      const result = await request(edit ? `${base}/ledgers/${encodeURIComponent(activeLedger.id)}` : `${base}/ledgers`, { method: edit ? "PUT" : "POST", headers: mutationHeaders(edit ? activeLedgerETag : ""), body: JSON.stringify(body) });
      form.closest("dialog").close(); announce(edit ? "Ledger revised." : "Ledger created."); await loadLedgers(result.body.id);
    } catch (error) { formError(form, error.message); } finally { submit.disabled = false; }
  });

  document.getElementById("finance-close-period")?.addEventListener("click", async (event) => {
    const form = event.currentTarget.form; const through = form.elements.close_through; const proof = form.elements.close_evidence;
    if (!through.value || !proof.value || !through.reportValidity() || !proof.reportValidity()) { formError(form, "A close date and evidence ID are required."); return; }
    event.currentTarget.disabled = true; formError(form);
    try {
      const result = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}/period-closes`, { method: "POST", headers: mutationHeaders(activeLedgerETag), body: JSON.stringify({ through: dateISO(through.value), evidence: [proof.value] }) });
      activeLedgerETag = result.etag; form.closest("dialog").close(); announce("Ledger period closed with evidence."); await loadLedgers(activeLedger.id);
    } catch (error) { formError(form, error.message); } finally { event.currentTarget.disabled = false; }
  });

  document.getElementById("finance-archive-ledger")?.addEventListener("click", async (event) => {
    if (!window.confirm("Archive this ledger? Its history remains readable.")) return;
    const form = event.currentTarget.form; event.currentTarget.disabled = true; formError(form);
    try {
      await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}`, { method: "DELETE", headers: mutationHeaders(activeLedgerETag, false) });
      form.closest("dialog").close(); announce("Ledger archived."); await loadLedgers();
    } catch (error) { formError(form, error.message); } finally { event.currentTarget.disabled = false; }
  });

  document.getElementById("finance-account-form")?.addEventListener("submit", async (event) => {
    event.preventDefault(); const form = event.currentTarget; const submit = event.submitter; submit.disabled = true; formError(form);
    try {
      const editing = Boolean(form.dataset.accountId);
      const body = { code: form.elements.code.value, name: form.elements.name.value, description: form.elements.description.value, allow_posting: form.elements.allow_posting.checked };
      if (!editing) body.type = form.elements.type.value;
      const path = editing ? `${base}/accounts/${encodeURIComponent(form.dataset.accountId)}` : `${base}/ledgers/${encodeURIComponent(activeLedger.id)}/accounts`;
      const result = await request(path, { method: editing ? "PUT" : "POST", headers: mutationHeaders(editing ? form.dataset.etag : ""), body: JSON.stringify(body) });
      form.closest("dialog").close(); announce(editing ? "Posting account revised." : "Posting account added."); await loadAccounts(); await loadAccount(result.body.id);
    } catch (error) { formError(form, error.message); } finally { submit.disabled = false; }
  });

  document.getElementById("finance-entry-form")?.addEventListener("submit", async (event) => {
    event.preventDefault(); const form = event.currentTarget; const submit = event.submitter; submit.disabled = true; formError(form);
    try {
      if (form.elements.debit_account.value === form.elements.credit_account.value) throw new Error("Debit and credit accounts must differ.");
      const amount = toMinor(form.elements.amount.value); const memo = form.elements.memo.value;
      const body = { entry_date: dateISO(form.elements.entry_date.value), description: form.elements.description.value, reference: form.elements.reference.value, currency: activeLedger.currency, lines: [{ account_id: form.elements.debit_account.value, memo, debit_minor: amount, credit_minor: 0 }, { account_id: form.elements.credit_account.value, memo, debit_minor: 0, credit_minor: amount }], evidence: evidence(form.elements.evidence.value) };
      const result = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}/entries`, { method: "POST", headers: mutationHeaders(), body: JSON.stringify(body) });
      form.closest("dialog").close(); announce("Balanced journal draft created."); await loadLedgers(activeLedger.id); await loadEntry(result.body.id);
    } catch (error) { formError(form, error.message); } finally { submit.disabled = false; }
  });

  document.getElementById("finance-reconciliation-form")?.addEventListener("submit", async (event) => {
    event.preventDefault(); const form = event.currentTarget; const submit = event.submitter; submit.disabled = true; formError(form);
    try {
      const body = { posting_account_id: form.elements.posting_account_id.value, as_of: dateISO(form.elements.as_of.value), statement_balance: { currency: activeLedger.currency, minor: toMinor(form.elements.statement_balance.value) }, evidence: [form.elements.evidence.value] };
      const result = await request(`${base}/ledgers/${encodeURIComponent(activeLedger.id)}/reconciliations`, { method: "POST", headers: mutationHeaders(), body: JSON.stringify(body) });
      form.closest("dialog").close(); announce(result.body.difference_minor === 0 ? "Zero-difference reconciliation proposed." : "Discrepancy recorded for review."); await loadReconciliations(); await loadReconciliation(result.body.id);
    } catch (error) { formError(form, error.message); } finally { submit.disabled = false; }
  });

  loadLedgers();
})();
