(() => {
  const settleTranscripts = (root = document) => {
    const transcripts = root.matches?.("[data-input-transcript]")
      ? [root]
      : root.querySelectorAll?.("[data-input-transcript]") || [];
    for (const transcript of transcripts) {
      transcript.scrollTop = transcript.scrollHeight;
      transcript.classList.add("is-ready");
    }
  };

  const setText = (selector, value) => {
    const element = document.querySelector(selector);
    if (element) element.textContent = String(value);
  };

  const formatTime = (value) => {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "Just now";
    return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  };

  const appendMessages = (messages = []) => {
    const transcript = document.querySelector("[data-input-transcript]");
    if (!transcript) return;

    const renderedCount = Number.parseInt(transcript.dataset.messageCount || "0", 10);
    const newMessages = messages.slice(Number.isNaN(renderedCount) ? 0 : renderedCount);
    for (const message of newMessages) {
      const bubble = document.createElement("div");
      const role = message.Role === "user" ? "user" : "agent";
      bubble.className = `coordinator-message coordinator-message-${role}`;

      const meta = document.createElement("div");
      meta.className = "coordinator-message-meta";
      const author = document.createElement("strong");
      author.textContent = role === "user" ? "You" : "Mia";
      const time = document.createElement("time");
      time.textContent = formatTime(message.CreatedAt);
      meta.append(author, time);

      const body = document.createElement("p");
      body.textContent = message.Body || "";
      bubble.append(meta, body);
      transcript.append(bubble);
    }

    transcript.dataset.messageCount = String(messages.length);
    if (newMessages.length) transcript.scrollTo({ top: transcript.scrollHeight, behavior: "smooth" });
  };

  const updateImpact = (form, tickets = []) => {
    let impact = form.querySelector("[data-current-impact]");
    if (!impact && tickets.length) {
      impact = document.createElement("div");
      impact.className = "coordinator-impact";
      impact.dataset.currentImpact = "";
      form.querySelector("footer")?.before(impact);
    }
    if (!impact) return;

    impact.replaceChildren();
    impact.classList.toggle("is-hidden", tickets.length === 0);
    if (!tickets.length) return;

    const heading = document.createElement("strong");
    heading.textContent = "This answer helps:";
    impact.append(heading);
    for (const ticket of tickets) {
      const link = document.createElement("a");
      link.href = `/work/${encodeURIComponent(ticket.ID)}`;
      link.textContent = `#${String(ticket.Number).padStart(4, "0")} ${ticket.Title}`;
      impact.append(link);
    }
  };

  const updateKnownFacts = (facts = []) => {
    const list = document.querySelector("[data-known-fact-list]");
    if (!list) return;
    list.replaceChildren();
    for (const fact of facts) {
      const row = document.createElement("div");
      const label = document.createElement("strong");
      label.textContent = fact.Label;
      const value = document.createElement("p");
      value.textContent = fact.Value;
      const source = document.createElement("small");
      const date = new Date(fact.UpdatedAt);
      const updated = Number.isNaN(date.getTime())
        ? "recently"
        : date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
      source.textContent = `${fact.SourceType} · updated ${updated}`;
      row.append(label, value, source);
      list.append(row);
    }
  };

  const updateQuestion = (current) => {
    const form = document.querySelector("[data-coordinator-form]");
    const complete = document.querySelector("[data-coordinator-complete]");
    if (!form) return;
    if (!current) {
      form.classList.add("is-hidden");
      form.setAttribute("aria-hidden", "true");
      complete?.classList.remove("is-hidden");
      return;
    }

    const factKey = form.querySelector("[data-current-fact-key]");
    const label = form.querySelector("[data-current-label]");
	const prompt = form.querySelector("[data-current-prompt]");
    const compose = form.querySelector("[data-input-compose]");
    const changedQuestion = factKey?.value !== current.FactKey;
    form.classList.remove("is-hidden");
    form.setAttribute("aria-hidden", "false");
    complete?.classList.add("is-hidden");
    if (factKey) factKey.value = current.FactKey;
    if (label) label.textContent = current.Label;
	if (prompt) prompt.textContent = current.Prompt || "Please provide the information Mia needs to continue this work.";
    if (changedQuestion) {
      if (compose) compose.value = "";
      form.querySelectorAll('input[type="checkbox"]').forEach((input) => { input.checked = false; });
      form.querySelectorAll('input[type="file"]').forEach((input) => { input.value = ""; });
      form.querySelectorAll("details[open]").forEach((details) => { details.open = false; });
    }
    updateImpact(form, current.Tickets || []);
  };

  const updateCoordinator = ({ state, counts }) => {
    appendMessages(state.Messages || []);
    setText('[data-coordinator-stat="requests"]', state.PendingRequests);
    setText('[data-coordinator-stat="topics"]', state.RemainingTopics);
    setText('[data-coordinator-stat="facts"]', state.KnownFacts);
    setText('[data-coordinator-stat="questions"]', state.PendingQuestions);
    setText('[data-coordinator-stat="sidebar-topics"]', state.RemainingTopics);
    setText("[data-your-turn-input-count]", counts.inputs);
    setText("[data-your-turn-review-count]", counts.reviews);
    setText("[data-your-turn-approval-count]", counts.approvals);
    updateKnownFacts(state.RecentFacts || []);
    updateQuestion(state.Current);
  };

  const problemMessage = async (response) => {
    if (response.headers.get("content-type")?.includes("application/json")) {
      const problem = await response.json();
      return problem.error?.detail || problem.detail || problem.title || "Mia could not save that answer.";
    }
    return "Mia could not save that answer. Please try again.";
  };

  settleTranscripts();

  let coordinatorRequestActive = false;
  const refreshCoordinator = async () => {
    if (coordinatorRequestActive || document.hidden || !document.querySelector("[data-input-transcript]")) return;
    coordinatorRequestActive = true;
    try {
      const response = await fetch("/your-turn/coordinator/state", { headers: { Accept: "application/json" } });
      if (response.ok) updateCoordinator(await response.json());
    } finally {
      coordinatorRequestActive = false;
    }
  };
  window.setInterval(refreshCoordinator, 5000);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refreshCoordinator();
  });

  document.addEventListener("keydown", (event) => {
    const compose = event.target.closest?.("[data-input-compose]");
    if (!compose || event.key !== "Enter" || event.shiftKey || event.isComposing) return;
    event.preventDefault();
    if (compose.value.trim()) compose.form.requestSubmit();
  });

  document.addEventListener("submit", async (event) => {
    const form = event.target.closest?.("[data-coordinator-form]");
    if (!form) return;
    event.preventDefault();
	coordinatorRequestActive = true;

    const savedScroll = { top: window.scrollY, left: window.scrollX };
    const error = form.querySelector("[data-coordinator-error]");
    const controls = [...form.querySelectorAll("button, textarea, input, select")];
    const formData = new FormData(form);
    if (event.submitter?.name) formData.set(event.submitter.name, event.submitter.value);
    if (error) {
      error.hidden = true;
      error.textContent = "";
    }
    form.setAttribute("aria-busy", "true");
    controls.forEach((control) => { control.disabled = true; });

    try {
      const response = await fetch(form.action, {
        method: "POST",
        body: formData,
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error(await problemMessage(response));
      updateCoordinator(await response.json());
      window.history.replaceState(null, "", "/your-turn?tab=input&status=coordinated");
    } catch (submitError) {
      if (error) {
        error.textContent = submitError.message || "Mia could not save that answer. Please try again.";
        error.hidden = false;
      }
    } finally {
      if (form.isConnected) {
        form.removeAttribute("aria-busy");
        controls.forEach((control) => { control.disabled = false; });
        form.querySelector("[data-input-compose]")?.focus({ preventScroll: true });
      }
	  coordinatorRequestActive = false;
      window.scrollTo({ ...savedScroll, behavior: "instant" });
    }
  });
})();
