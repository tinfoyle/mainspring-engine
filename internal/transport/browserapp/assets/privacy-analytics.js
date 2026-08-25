(() => {
  "use strict";
  const panel = document.getElementById("privacy-consent");
  const reopen = document.getElementById("privacy-reopen");
  const options = document.getElementById("privacy-options");
  const analytics = document.getElementById("privacy-analytics");
  const marketing = document.getElementById("privacy-marketing");
  const error = document.getElementById("privacy-error");
  const marker = document.getElementById("analytics-marker");
  if (!panel || !reopen || !options || !analytics || !marketing || !error) return;
  let decision;
  let emitted = false;
  let emitting = false;
  let retryCount = 0;
  let retryTimer;

  const occurredAt = marker?.dataset.occurredAt || new Date().toISOString();
  const events = [
    { name: marker?.dataset.event, id: marker?.dataset.eventId },
    { name: marker?.dataset.eventSecond, id: marker?.dataset.eventSecondId }
  ].filter((event) => event.name).map((event) => ({
    id: event.id || crypto.randomUUID(),
    name: event.name,
    occurredAt
  }));
  const deliveryKey = marker && events.length
    ? `spyglass:onboarding-analytics:v1:${events.map((event) => event.id).join("+")}`
    : "";

  const wasHandled = () => {
    if (!deliveryKey) return false;
    try { return sessionStorage.getItem(deliveryKey) === "1"; } catch { return false; }
  };
  const rememberHandled = () => {
    if (!deliveryKey) return;
    try { sessionStorage.setItem(deliveryKey, "1"); } catch { /* Optional deduplication can remain memory-only. */ }
  };

  const fieldsFor = (name) => {
    const fields = {};
    if (marker?.dataset.offer && ["registration_started"].includes(name)) fields.offer_code = marker.dataset.offer;
    if (name === "security_enrollment_completed") fields.method = marker?.dataset.method || "passkey_recovery_codes";
    return fields;
  };
  const emit = async () => {
    if (emitted || emitting || !decision?.decided || !decision.analytics || decision.renewal_required || events.length === 0) return;
    if (wasHandled()) {
      emitted = true;
      return;
    }
    emitting = true;
    const results = await Promise.allSettled(events.map((event) => fetch("/api/v1/analytics/events", {
      method: "POST", credentials: "same-origin", keepalive: true,
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ event_id: event.id, name: event.name, occurred_at: event.occurredAt, fields: fieldsFor(event.name) })
    })));
    emitting = false;
    if (results.every((result) => result.status === "fulfilled" && result.value.ok)) {
      emitted = true;
      rememberHandled();
      return;
    }
    const responses = results.flatMap((result) => result.status === "fulfilled" ? [result.value] : []);
    if (responses.some((response) => response.status === 401 || response.status === 403)) {
      emitted = true;
      rememberHandled();
      return;
    }
    const retryable = results.some((result) => result.status === "rejected") || responses.some((response) => response.status === 429 || response.status >= 500);
    if (retryable && retryCount < 2) {
      retryCount += 1;
      retryTimer = window.setTimeout(() => void emit(), retryCount * 1000);
    } else {
      emitted = true;
      rememberHandled();
    }
  };
  const render = () => {
    const needsChoice = !decision?.decided || decision.renewal_required;
    panel.hidden = !needsChoice;
    reopen.hidden = needsChoice;
    analytics.checked = Boolean(decision?.analytics);
    marketing.checked = Boolean(decision?.marketing);
    if (!needsChoice) void emit();
  };
  const save = async (analyticsChoice, marketingChoice) => {
    const previous = decision;
    error.textContent = "";
    try {
      const response = await fetch("/api/v1/privacy/consent", {
        method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json", "Accept": "application/json" },
        body: JSON.stringify({ analytics: analyticsChoice, marketing: marketingChoice })
      });
      if (!response.ok) throw new Error("preference rejected");
      decision = await response.json();
      options.hidden = true;
      render();
    } catch {
      decision = previous || { decided: false, analytics: false, marketing: false, renewal_required: false };
      analytics.checked = Boolean(decision.analytics);
      marketing.checked = Boolean(decision.marketing);
      panel.hidden = false;
      reopen.hidden = true;
      options.hidden = false;
      error.textContent = decision.decided && !decision.renewal_required
        ? "We could not save that change. Your previous choice remains in effect; please try again."
        : "We could not save that choice. Optional tracking remains off; please try again.";
    }
  };
  panel.querySelector("[data-privacy-accept]")?.addEventListener("click", () => void save(true, false));
  panel.querySelector("[data-privacy-reject]")?.addEventListener("click", () => void save(false, false));
  panel.querySelector("[data-privacy-manage]")?.addEventListener("click", () => { options.hidden = false; });
  panel.querySelector("[data-privacy-save]")?.addEventListener("click", () => void save(analytics.checked, marketing.checked));
  reopen.addEventListener("click", () => { panel.hidden = false; reopen.hidden = true; options.hidden = false; });
  window.addEventListener("online", () => void emit());
  document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") void emit(); });
  window.addEventListener("pagehide", () => { if (retryTimer) window.clearTimeout(retryTimer); }, { once: true });

  fetch("/api/v1/privacy/consent", { credentials: "same-origin", headers: { "Accept": "application/json" } })
    .then((response) => { if (!response.ok) throw new Error("preference unavailable"); return response.json(); })
    .then((current) => { decision = current; render(); })
    .catch(() => { decision = { decided: false, analytics: false, marketing: false }; error.textContent = "Privacy choices are temporarily unavailable. Optional tracking remains off."; render(); });
})();
