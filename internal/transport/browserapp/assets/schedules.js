(() => {
  "use strict";

  const root = document.getElementById("schedules-app");
  if (!root) return;

  const accountID = root.dataset.accountId;
  const readOnly = root.dataset.readOnly === "true";
  const baseURL = `/api/v1/accounts/${encodeURIComponent(accountID)}`;
  const list = document.getElementById("schedules-list");
  const status = document.getElementById("schedules-status");
  const count = document.getElementById("schedules-count");
  const more = document.getElementById("schedules-more");
  const form = document.getElementById("schedule-form");
  const boardroom = document.getElementById("schedule-boardroom");
  const personaPicker = document.getElementById("schedule-personas");
  const weekdayPicker = document.getElementById("schedule-weekdays");
  const formLabel = document.getElementById("schedule-form-label");
  const formTitle = document.getElementById("schedule-form-title");
  const formError = document.getElementById("schedule-form-error");
  const submit = document.getElementById("schedule-submit");
  const cancel = document.getElementById("schedule-cancel");

  let schedules = [];
  let cursor = "";
  let rooms = [];
  let personas = [];
  let editing = null;
  const pendingOperations = new Map();

  function element(tag, className, text) {
    const value = document.createElement(tag);
    if (className) value.className = className;
    if (text !== undefined) value.textContent = text;
    return value;
  }

  function dateTime(value) {
    if (!value) return "Paused";
    const instant = new Date(value);
    if (Number.isNaN(instant.valueOf())) return "Unknown time";
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short", timeZoneName: "short" }).format(instant);
  }

  function recurrenceLabel(item) {
    const recurrence = item.recurrence || {};
    const time = `${String(recurrence.local_hour || 0).padStart(2, "0")}:${String(recurrence.local_minute || 0).padStart(2, "0")}`;
    if (recurrence.frequency === "weekly") {
      const labels = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
      return `${(recurrence.weekdays || []).map((day) => labels[day]).join(", ")} at ${time}`;
    }
    return `Daily at ${time}`;
  }

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, {
      ...options,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(options.headers || {}) },
    });
    let body = null;
    try { body = await response.json(); } catch { /* stable fallback below */ }
    if (!response.ok) {
      const error = new Error(body?.detail || "Spyglass could not complete this Schedule operation.");
      error.status = response.status;
      error.code = body?.code || "schedules_unavailable";
      throw error;
    }
    return body;
  }

  async function mutation(url, method, value) {
    const body = JSON.stringify(value);
    const fingerprint = `${method} ${url} ${body}`;
    let operationID = pendingOperations.get(fingerprint);
    if (!operationID) {
      operationID = crypto.randomUUID();
      pendingOperations.set(fingerprint, operationID);
    }
    try {
      const result = await requestJSON(url, {
        method,
        headers: { "Content-Type": "application/json", "Idempotency-Key": operationID },
        body,
      });
      pendingOperations.delete(fingerprint);
      return result;
    } catch (error) {
      if (error.status && error.status < 500) pendingOperations.delete(fingerprint);
      throw error;
    }
  }

  function actionButton(label, className, action) {
    const button = element("button", className, label);
    button.type = "button";
    button.addEventListener("click", action);
    return button;
  }

  function scheduleCard(item) {
    const card = element("article", "schedule-card");
    card.dataset.scheduleId = item.id;
    const heading = element("header");
    const title = element("div");
    title.append(element("strong", "", item.name), element("small", "", `${recurrenceLabel(item)} · ${item.timezone}`));
    heading.append(title, element("em", `schedule-state state-${item.state}`, item.state));
    const details = element("dl");
    details.append(
      description("Next run", dateTime(item.next_run_at)),
      description("Boardroom", roomName(item.template?.boardroom_id)),
      description("Policy", item.missed_run_policy === "catch_up_one" ? "Catch up one missed run" : "Skip missed backlog"),
      description("Version", `v${item.version}`),
    );
    card.append(heading, details);
    if (!readOnly) {
      const actions = element("footer", "schedule-actions");
      actions.append(actionButton("Edit", "secondary", () => beginEdit(item)));
      if (item.state === "active") {
        actions.append(actionButton("Run now", "primary", () => triggerNow(item)));
        actions.append(actionButton("Pause", "secondary", () => transition(item, "pauses")));
      }
      if (item.state === "paused") actions.append(actionButton("Resume", "secondary", () => transition(item, "resumptions")));
      actions.append(actionButton("Delete", "danger", () => remove(item)));
      card.append(actions);
    }
    return card;
  }

  function description(term, value) {
    const wrapper = element("div");
    wrapper.append(element("dt", "", term), element("dd", "", value));
    return wrapper;
  }

  function roomName(id) {
    return rooms.find((item) => item.id === id)?.name || "Configured Boardroom";
  }

  function renderSchedules() {
    list.replaceChildren(...schedules.map(scheduleCard));
    count.textContent = `${schedules.length} loaded`;
    status.hidden = schedules.length > 0;
    status.textContent = schedules.length ? "" : "No Schedules have been created for this Account.";
    more.hidden = !cursor;
  }

  async function loadSchedules(append = false) {
    root.setAttribute("aria-busy", "true");
    status.hidden = false;
    status.classList.remove("error");
    status.textContent = append ? "Loading more Schedules…" : "Loading Schedules…";
    const query = new URLSearchParams({ limit: "30" });
    if (append && cursor) query.set("cursor", cursor);
    try {
      const page = await requestJSON(`${baseURL}/schedules?${query}`);
      schedules = append ? schedules.concat(page.items || []) : (page.items || []);
      cursor = page.next_cursor || "";
      renderSchedules();
    } catch (error) {
      status.hidden = false;
      status.classList.add("error");
      status.textContent = error.status === 403 ? "Current package access does not allow Schedule reads." : error.message;
      count.textContent = "Unavailable";
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }

  async function loadRooms() {
    if (!form) return;
    try {
      const page = await requestJSON(`${baseURL}/agent-boardrooms`);
      rooms = (page.items || []).filter((item) => item.state === "active");
      boardroom.replaceChildren(...rooms.map((room) => {
        const option = document.createElement("option");
        option.value = room.id;
        option.textContent = room.name;
        return option;
      }));
      if (!rooms.length) {
        const option = document.createElement("option");
        option.value = "";
        option.textContent = "Create an active Boardroom first";
        boardroom.append(option);
        submit.disabled = true;
        personaPicker.replaceChildren(element("p", "", "No active Boardroom is available."));
        return;
      }
      await loadPersonas(boardroom.value);
    } catch (error) {
      boardroom.replaceChildren();
      const option = document.createElement("option");
      option.value = "";
      option.textContent = "Boardrooms unavailable";
      boardroom.append(option);
      submit.disabled = true;
      showFormError(error.message);
    }
  }

  async function loadPersonas(boardroomID, selected = []) {
    personaPicker.replaceChildren(element("p", "", "Loading Personas…"));
    if (!boardroomID) return;
    try {
      const page = await requestJSON(`${baseURL}/agent-boardrooms/${encodeURIComponent(boardroomID)}/personas`);
      personas = (page.items || []).filter((item) => item.state === "active");
      personaPicker.replaceChildren(...personas.map((persona) => {
        const label = element("label");
        const checkbox = document.createElement("input");
        checkbox.type = "checkbox";
        checkbox.value = persona.id;
        checkbox.checked = selected.includes(persona.id);
        label.append(checkbox, document.createTextNode(`${persona.name} · ${persona.role}`));
        return label;
      }));
      if (!personas.length) personaPicker.replaceChildren(element("p", "", "No active published Personas are available in this Boardroom."));
    } catch (error) {
      personas = [];
      personaPicker.replaceChildren(element("p", "error", error.message));
    }
  }

  function selectedValues(container) {
    return [...container.querySelectorAll('input[type="checkbox"]:checked')].map((input) => input.value);
  }

  function idList(raw) {
    return String(raw || "").split(",").map((value) => value.trim()).filter(Boolean);
  }

  function field(name) {
    return form.elements.namedItem(name);
  }

  function definition() {
    const [hour, minute] = String(field("local_time").value || "00:00").split(":").map(Number);
    return {
      name: field("name").value,
      timezone: field("timezone").value,
      recurrence: {
        frequency: field("frequency").value,
        local_hour: hour,
        local_minute: minute,
        weekdays: field("frequency").value === "weekly" ? selectedValues(weekdayPicker).map(Number) : [],
        gap_policy: field("gap_policy").value,
        overlap_policy: field("overlap_policy").value,
      },
      missed_run_policy: field("missed_run_policy").value,
      template: {
        boardroom_id: boardroom.value,
        mode: field("mode").value,
        persona_ids: selectedValues(personaPicker),
        subject: field("subject").value,
        prompt: field("prompt").value,
        work_item_ids: idList(field("work_item_ids").value),
        knowledge_fact_ids: idList(field("knowledge_fact_ids").value),
        knowledge_document_ids: idList(field("knowledge_document_ids").value),
        baseline_assessment_ids: idList(field("baseline_assessment_ids").value),
      },
      reason: field("reason").value,
    };
  }

  function showFormError(message) {
    if (!formError) return;
    formError.hidden = false;
    formError.textContent = message;
  }

  function resetForm() {
    if (!form) return;
    editing = null;
    form.reset();
    field("timezone").value = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
    field("local_time").value = "09:00";
    field("frequency").value = "daily";
    weekdayPicker.hidden = true;
    formLabel.textContent = "NEW SCHEDULE";
    formTitle.textContent = "Create a recurring Run";
    submit.textContent = "Create Schedule →";
    cancel.hidden = true;
    formError.hidden = true;
    if (rooms.length) {
      boardroom.value = rooms[0].id;
      loadPersonas(boardroom.value);
    }
  }

  async function beginEdit(item) {
    editing = item;
    formLabel.textContent = "EDIT SCHEDULE";
    formTitle.textContent = item.name;
    submit.textContent = "Save revision →";
    cancel.hidden = false;
    formError.hidden = true;
    field("expected_version").value = String(item.version);
    field("name").value = item.name;
    field("timezone").value = item.timezone;
    field("frequency").value = item.recurrence.frequency;
    field("local_time").value = `${String(item.recurrence.local_hour).padStart(2, "0")}:${String(item.recurrence.local_minute).padStart(2, "0")}`;
    field("gap_policy").value = item.recurrence.gap_policy;
    field("overlap_policy").value = item.recurrence.overlap_policy;
    field("missed_run_policy").value = item.missed_run_policy;
    field("mode").value = item.template.mode;
    field("subject").value = item.template.subject;
    field("prompt").value = item.template.prompt;
    field("work_item_ids").value = (item.template.work_item_ids || []).join(", ");
    field("knowledge_fact_ids").value = (item.template.knowledge_fact_ids || []).join(", ");
    field("knowledge_document_ids").value = (item.template.knowledge_document_ids || []).join(", ");
    field("baseline_assessment_ids").value = (item.template.baseline_assessment_ids || []).join(", ");
    field("reason").value = "";
    weekdayPicker.hidden = item.recurrence.frequency !== "weekly";
    for (const checkbox of weekdayPicker.querySelectorAll('input[type="checkbox"]')) checkbox.checked = (item.recurrence.weekdays || []).includes(Number(checkbox.value));
    boardroom.value = item.template.boardroom_id;
    await loadPersonas(boardroom.value, item.template.persona_ids || []);
    form.scrollIntoView({ behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
  }

  async function transition(item, endpoint) {
    try {
      await mutation(`${baseURL}/schedules/${encodeURIComponent(item.id)}/${endpoint}`, "POST", { expected_version: item.version, reason: endpoint === "pauses" ? "Paused from Schedule workspace" : "Resumed from Schedule workspace" });
      await loadSchedules(false);
    } catch (error) {
      status.hidden = false;
      status.classList.add("error");
      status.textContent = error.code === "schedule_operation_conflict" ? "This Schedule changed elsewhere. The list has been refreshed." : error.message;
      await loadSchedules(false);
    }
  }

  async function triggerNow(item) {
    try {
      const accepted = await mutation(`${baseURL}/schedules/${encodeURIComponent(item.id)}/triggers`, "POST", { expected_version: item.version, reason: "Triggered from Schedule workspace" });
      status.hidden = false;
      status.classList.remove("error");
      status.textContent = `“${item.name}” was accepted as a separate Run at ${dateTime(accepted.requested_at)}. Its recurrence did not move.`;
    } catch (error) {
      status.hidden = false;
      status.classList.add("error");
      status.textContent = error.code === "schedule_operation_conflict" ? "This Schedule changed before the trigger was accepted. Refresh and try again." : error.message;
    }
  }

  async function remove(item) {
    if (!window.confirm(`Delete “${item.name}”? Its occurrence history is retained, but it can never run again.`)) return;
    try {
      await mutation(`${baseURL}/schedules/${encodeURIComponent(item.id)}`, "DELETE", { expected_version: item.version, reason: "Deleted from Schedule workspace" });
      if (editing?.id === item.id) resetForm();
      await loadSchedules(false);
    } catch (error) {
      status.hidden = false;
      status.classList.add("error");
      status.textContent = error.message;
    }
  }

  if (more) more.addEventListener("click", () => loadSchedules(true));
  if (form) {
    field("timezone").value = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
    field("frequency").addEventListener("change", () => { weekdayPicker.hidden = field("frequency").value !== "weekly"; });
    boardroom.addEventListener("change", () => loadPersonas(boardroom.value));
    cancel.addEventListener("click", resetForm);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      formError.hidden = true;
      const body = definition();
      if (!body.template.persona_ids.length) {
        showFormError("Select at least one active Persona.");
        return;
      }
      if (body.recurrence.frequency === "weekly" && !body.recurrence.weekdays.length) {
        showFormError("Select at least one weekday.");
        return;
      }
      submit.disabled = true;
      try {
        if (editing) {
          body.expected_version = editing.version;
          await mutation(`${baseURL}/schedules/${encodeURIComponent(editing.id)}`, "PUT", body);
        } else {
          await mutation(`${baseURL}/schedules`, "POST", body);
        }
        resetForm();
        await loadSchedules(false);
      } catch (error) {
        showFormError(error.code === "schedule_operation_conflict" ? "This Schedule changed elsewhere. Refresh and apply the revision again." : error.message);
      } finally {
        submit.disabled = rooms.length === 0;
      }
    });
  }

  Promise.all([loadSchedules(false), loadRooms()]).finally(() => root.setAttribute("aria-busy", "false"));
})();
