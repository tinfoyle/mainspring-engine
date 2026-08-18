(() => {
  "use strict";

  const root = document.getElementById("agents-app");
  if (!root) return;

  const accountID = root.dataset.accountId;
  const readOnly = root.dataset.readOnly === "true";
  const baseURL = `/api/v1/accounts/${encodeURIComponent(accountID)}`;
  const roomList = document.getElementById("agents-room-list");
  const roomStatus = document.getElementById("agents-room-status");
  const roomCount = document.getElementById("agents-room-count");
  const roomName = document.getElementById("agents-room-name");
  const roomPurpose = document.getElementById("agents-room-purpose");
  const roomPolicy = document.getElementById("agents-room-policy");
  const personasNode = document.getElementById("agents-personas");
  const conversationList = document.getElementById("agents-conversation-list");
  const conversationStatus = document.getElementById("agents-conversation-status");
  const conversationCount = document.getElementById("agents-conversation-count");
  const moreConversations = document.getElementById("agents-more-conversations");
  const transcript = document.getElementById("agents-transcript");
  const transcriptTitle = document.getElementById("agents-transcript-title");
  const transcriptState = document.getElementById("agents-transcript-state");
  const messageList = document.getElementById("agents-message-list");
  const moreMessages = document.getElementById("agents-more-messages");
  const runStatus = document.getElementById("agents-run-status");
  const form = document.getElementById("agents-run-form");
  const personaPicker = document.getElementById("agents-persona-picker");
  const subjectField = document.getElementById("agents-subject-field");
  const composeLabel = document.getElementById("agents-compose-label");
  const composeTitle = document.getElementById("agents-compose-title");
  const newConversation = document.getElementById("agents-new-conversation");
  const formError = document.getElementById("agents-form-error");

  let selectedRoom = null;
  let selectedConversation = null;
  let personas = [];
  let conversationCursor = "";
  let messageCursor = "";
  let loadedConversations = 0;
  let loadGeneration = 0;
  const pendingOperations = new Map();

  function node(tag, className, text) {
    const value = document.createElement(tag);
    if (className) value.className = className;
    if (text !== undefined) value.textContent = text;
    return value;
  }

  function initials(name) {
    return String(name || "Agent").split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
  }

  function dateLabel(value) {
    const date = new Date(value);
    if (Number.isNaN(date.valueOf())) return "Unknown time";
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
  }

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, {
      ...options,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(options.headers || {}) },
    });
    if (!response.ok) {
      let problem = {};
      try { problem = await response.json(); } catch { /* use the stable fallback */ }
      const error = new Error(problem.detail || "Spyglass could not load this Agent workspace right now.");
      error.status = response.status;
      error.code = problem.code || "agents_unavailable";
      throw error;
    }
    return response.json();
  }

  function roomCard(room) {
    const button = node("button", "agents-room-card");
    button.type = "button";
    button.setAttribute("aria-pressed", "false");
    button.dataset.roomId = room.id;
    const marker = node("span", "agents-room-marker", initials(room.name));
    const copy = node("span", "agents-room-copy");
    copy.append(node("strong", "", room.name), node("small", "", room.purpose));
    button.append(marker, copy, node("em", `state-${room.state}`, room.state));
    button.addEventListener("click", () => selectRoom(room, button));
    return button;
  }

  async function loadRooms() {
    root.setAttribute("aria-busy", "true");
    try {
      const page = await requestJSON(`${baseURL}/agent-boardrooms`);
      const rooms = page.items || [];
      roomList.replaceChildren(...rooms.map(roomCard));
      roomCount.textContent = `${rooms.length} ${rooms.length === 1 ? "room" : "rooms"}`;
      roomStatus.hidden = rooms.length > 0;
      roomStatus.textContent = rooms.length ? "" : "No Boardrooms have been configured for this Account.";
      if (rooms.length) {
        const first = roomList.querySelector("button");
        await selectRoom(rooms[0], first);
      }
    } catch (error) {
      roomStatus.hidden = false;
      roomStatus.classList.add("error");
      roomStatus.textContent = error.status === 403
        ? "This Account's Agents access is no longer available. Refresh to update the package view."
        : error.message;
      roomCount.textContent = "Unavailable";
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }

  async function selectRoom(room, button) {
    const generation = ++loadGeneration;
    selectedRoom = room;
    selectedConversation = null;
    conversationCursor = "";
    messageCursor = "";
    loadedConversations = 0;
    for (const current of roomList.querySelectorAll("button")) {
      const active = current === button;
      current.classList.toggle("selected", active);
      current.setAttribute("aria-pressed", String(active));
    }
    roomName.textContent = room.name;
    roomPurpose.textContent = room.purpose;
    roomPolicy.textContent = `${room.state} · policy v${room.version}`;
    personasNode.replaceChildren(node("p", "agents-muted", "Loading Persona versions…"));
    conversationList.replaceChildren();
    conversationStatus.hidden = false;
    conversationStatus.classList.remove("error");
    conversationStatus.textContent = "Loading conversations…";
    conversationCount.textContent = "Loading";
    transcript.hidden = true;
    if (form) form.hidden = true;
    try {
      const [personaPage] = await Promise.all([
        requestJSON(`${baseURL}/agent-boardrooms/${encodeURIComponent(room.id)}/personas`),
        loadConversations(false, generation),
      ]);
      if (generation !== loadGeneration) return;
      personas = personaPage.items || [];
      renderPersonas();
      if (form) {
        form.hidden = personas.filter((item) => item.state === "active").length === 0;
        resetComposer();
      }
    } catch (error) {
      if (generation !== loadGeneration) return;
      personas = [];
      personasNode.replaceChildren(node("p", "agents-muted error", error.message));
      if (form) form.hidden = true;
    }
  }

  function renderPersonas() {
    personasNode.replaceChildren();
    if (!personas.length) {
      personasNode.append(node("p", "agents-muted", "No published Personas are available in this Boardroom."));
      return;
    }
    for (const persona of personas) {
      const card = node("article", `agents-persona ${persona.state !== "active" ? "inactive" : ""}`);
      const avatar = node("span", "", initials(persona.name));
      const copy = node("div");
      copy.append(node("strong", "", persona.name), node("small", "", persona.role));
      const version = node("em", "", `v${persona.latest_version}`);
      card.append(avatar, copy, version);
      personasNode.append(card);
    }
  }

  function conversationCard(item) {
    const button = node("button", "agents-conversation-card");
    button.type = "button";
    button.setAttribute("aria-pressed", "false");
    button.dataset.conversationId = item.id;
    const signal = node("i", `state-${item.state}`);
    signal.setAttribute("aria-hidden", "true");
    const copy = node("span", "agents-conversation-copy");
    copy.append(node("strong", "", item.subject), node("small", "", `${dateLabel(item.updated_at)} · ${item.message_count} ${item.message_count === 1 ? "message" : "messages"}`));
    button.append(signal, copy, node("em", "", item.state));
    button.addEventListener("click", () => openConversation(item, button));
    return button;
  }

  async function loadConversations(append, expectedGeneration = loadGeneration) {
    if (!selectedRoom) return;
    const query = new URLSearchParams({ limit: "30" });
    if (append && conversationCursor) query.set("cursor", conversationCursor);
    if (!append) {
      conversationList.replaceChildren();
      loadedConversations = 0;
    }
    conversationStatus.hidden = false;
    conversationStatus.classList.remove("error");
    conversationStatus.textContent = append ? "Loading more conversations…" : "Loading conversations…";
    moreConversations.hidden = true;
    try {
      const page = await requestJSON(`${baseURL}/agent-boardrooms/${encodeURIComponent(selectedRoom.id)}/conversations?${query}`);
      if (expectedGeneration !== loadGeneration) return;
      for (const item of page.items || []) conversationList.append(conversationCard(item));
      loadedConversations += (page.items || []).length;
      conversationCursor = page.next_cursor || "";
      conversationCount.textContent = `${loadedConversations} loaded`;
      conversationStatus.textContent = loadedConversations ? "" : "No conversations yet. Convene the Boardroom with a clear question.";
      conversationStatus.hidden = loadedConversations > 0;
      moreConversations.hidden = !conversationCursor;
    } catch (error) {
      if (expectedGeneration !== loadGeneration) return;
      conversationStatus.hidden = false;
      conversationStatus.classList.add("error");
      conversationStatus.textContent = error.message;
      conversationCount.textContent = "Unavailable";
    }
  }

  function resetComposer() {
    if (!form) return;
    selectedConversation = null;
    form.reset();
    formError.hidden = true;
    subjectField.hidden = false;
    subjectField.querySelector("input").required = true;
    composeLabel.textContent = "NEW CONVERSATION";
    composeTitle.textContent = "Convene this Boardroom";
    newConversation.hidden = true;
    renderPersonaPicker();
  }

  function renderPersonaPicker() {
    if (!personaPicker) return;
    personaPicker.replaceChildren();
    for (const persona of personas.filter((item) => item.state === "active")) {
      const label = node("label", "agents-persona-option");
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.name = "persona_id";
      checkbox.value = persona.id;
      checkbox.checked = true;
      const avatar = node("span", "", initials(persona.name));
      const copy = node("i");
      copy.append(node("strong", "", persona.name), node("small", "", persona.role));
      label.append(checkbox, avatar, copy);
      personaPicker.append(label);
    }
  }

  function continueComposer(item) {
    if (!form) return;
    selectedConversation = item;
    formError.hidden = true;
    subjectField.hidden = true;
    subjectField.querySelector("input").required = false;
    composeLabel.textContent = "FOLLOW-UP";
    composeTitle.textContent = item.subject;
    newConversation.hidden = false;
    form.elements.prompt.value = "";
    renderPersonaPicker();
    form.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  async function openConversation(item, button) {
    selectedConversation = item;
    messageCursor = "";
    for (const current of conversationList.querySelectorAll("button")) {
      const active = current === button;
      current.classList.toggle("selected", active);
      current.setAttribute("aria-pressed", String(active));
    }
    transcript.hidden = false;
    transcriptTitle.textContent = item.subject;
    transcriptState.textContent = item.state;
    messageList.replaceChildren();
    moreMessages.hidden = true;
    if (form) continueComposer(item);
    await loadMessages(false);
  }

  function personaForMessage(message) {
    return personas.find((item) => item.persona_version_id === message.persona_version_id);
  }

  function resultSection(title, values) {
    if (!Array.isArray(values) || !values.length) return null;
    const section = node("section", "agents-result-section");
    section.append(node("strong", "", title));
    const list = document.createElement("ul");
    for (const value of values) list.append(node("li", "", typeof value === "string" ? value : value.reason || value.label || value.request || "Structured result"));
    section.append(list);
    return section;
  }

  function messageCard(message) {
    const persona = personaForMessage(message);
    const isUser = message.role === "user";
    const article = node("article", `agents-message ${isUser ? "user" : "persona"}`);
    const avatar = node("span", "agents-message-avatar", isUser ? "YOU" : initials(persona && persona.name));
    const body = node("div", "agents-message-body");
    const header = document.createElement("header");
    header.append(node("strong", "", isUser ? "You" : (persona && persona.name) || "Boardroom Persona"), node("time", "", dateLabel(message.created_at)));
    body.append(header, node("p", "agents-contribution", message.body));
    if (message.result) {
      const evidence = node("div", "agents-result");
      for (const section of [
        resultSection("Findings", message.result.findings),
        resultSection("Recommendations", message.result.recommendations),
        resultSection("Questions", message.result.questions),
        resultSection("Citations", message.result.citations),
        resultSection("Proposed actions", message.result.proposed_actions),
        resultSection("Delegations", message.result.delegations),
      ]) if (section) evidence.append(section);
      const confidence = node("small", "agents-confidence", `${message.result.confidence} confidence`);
      evidence.append(confidence);
      body.append(evidence);
    }
    article.append(avatar, body);
    return article;
  }

  async function loadMessages(append) {
    if (!selectedConversation) return;
    const conversationID = selectedConversation.id;
    const query = new URLSearchParams({ limit: "100" });
    if (append && messageCursor) query.set("cursor", messageCursor);
    if (!append) messageList.replaceChildren(node("p", "agents-muted agents-message-loading", "Loading ordered messages…"));
    moreMessages.hidden = true;
    try {
      const page = await requestJSON(`${baseURL}/agent-conversations/${encodeURIComponent(conversationID)}/messages?${query}`);
      if (!selectedConversation || selectedConversation.id !== conversationID) return;
      if (!append) messageList.replaceChildren();
      for (const message of page.items || []) messageList.append(messageCard(message));
      if (!messageList.children.length) messageList.append(node("p", "agents-muted agents-message-loading", "This conversation has no projected messages yet."));
      messageCursor = page.next_cursor || "";
      moreMessages.hidden = !messageCursor;
    } catch (error) {
      if (!selectedConversation || selectedConversation.id !== conversationID) return;
      if (!append) messageList.replaceChildren();
      messageList.append(node("p", "agents-muted error agents-message-loading", error.message));
    }
  }

  async function pollRun(run, generation, roomID) {
    runStatus.hidden = false;
    for (let attempt = 0; attempt < 40; attempt += 1) {
      if (generation !== loadGeneration || !selectedRoom || selectedRoom.id !== roomID) return;
      runStatus.textContent = `Boardroom run ${run.state.replaceAll("_", " ")}…`;
      if (["succeeded", "partially_failed", "failed", "canceled"].includes(run.state)) break;
      await new Promise((resolve) => window.setTimeout(resolve, 1500));
      if (generation !== loadGeneration || !selectedRoom || selectedRoom.id !== roomID) return;
      try {
        run = await requestJSON(`${baseURL}/agent-runs/${encodeURIComponent(run.id)}`);
      } catch (error) {
        runStatus.textContent = `${error.message} The durable run can be checked again from this conversation.`;
        return;
      }
    }
    runStatus.textContent = `Boardroom run ${run.state.replaceAll("_", " ")}.`;
    await Promise.all([loadMessages(false), loadConversations(false)]);
  }

  if (form && !readOnly) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!selectedRoom) return;
      const values = new FormData(form);
      const personaIDs = values.getAll("persona_id").map(String);
      if (!personaIDs.length) {
        formError.textContent = "Select at least one active Persona.";
        formError.hidden = false;
        return;
      }
      const payload = { prompt: String(values.get("prompt") || "").trim(), persona_ids: personaIDs };
      if (selectedConversation) payload.conversation_id = selectedConversation.id;
      else payload.subject = String(values.get("subject") || "").trim();
      const body = JSON.stringify(payload);
      const fingerprint = `${selectedRoom.id} ${body}`;
      let operationID = pendingOperations.get(fingerprint);
      if (!operationID) {
        operationID = crypto.randomUUID();
        pendingOperations.set(fingerprint, operationID);
      }
      const submit = form.querySelector('button[type="submit"]');
      submit.disabled = true;
      formError.hidden = true;
      try {
        const generation = loadGeneration;
        const roomID = selectedRoom.id;
        const run = await requestJSON(`${baseURL}/agent-boardrooms/${encodeURIComponent(selectedRoom.id)}/runs`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "Idempotency-Key": operationID },
          body,
        });
        pendingOperations.delete(fingerprint);
        const conversation = { id: run.conversation_id, subject: run.subject, state: "open", message_count: 1, updated_at: run.created_at };
        selectedConversation = conversation;
        transcript.hidden = false;
        transcriptTitle.textContent = conversation.subject;
        transcriptState.textContent = conversation.state;
        continueComposer(conversation);
        await loadMessages(false);
        void pollRun(run, generation, roomID);
      } catch (error) {
        if (error.status && error.status < 500) pendingOperations.delete(fingerprint);
        formError.textContent = error.status === 403
          ? "This Account cannot start Agent runs with its current package access."
          : error.message;
        formError.hidden = false;
      } finally {
        submit.disabled = false;
      }
    });
    newConversation.addEventListener("click", resetComposer);
  }

  moreConversations.addEventListener("click", () => loadConversations(true));
  moreMessages.addEventListener("click", () => loadMessages(true));
  void loadRooms();
})();
