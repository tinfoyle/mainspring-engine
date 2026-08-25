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

  const fieldsFor = (name) => {
    const fields = {};
    if (marker?.dataset.offer && ["registration_started"].includes(name)) fields.offer_code = marker.dataset.offer;
    if (name === "security_enrollment_completed") fields.method = marker?.dataset.method || "passkey_recovery_codes";
    return fields;
  };
  const emit = async () => {
    if (emitted || !decision?.decided || !decision.analytics || decision.renewal_required || !marker) return;
    emitted = true;
    const names = [marker.dataset.event, marker.dataset.eventSecond].filter(Boolean);
    await Promise.allSettled(names.map((name) => fetch("/api/v1/analytics/events", {
      method: "POST", credentials: "same-origin", keepalive: true,
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ event_id: crypto.randomUUID(), name, occurred_at: new Date().toISOString(), fields: fieldsFor(name) })
    })));
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

  fetch("/api/v1/privacy/consent", { credentials: "same-origin", headers: { "Accept": "application/json" } })
    .then((response) => { if (!response.ok) throw new Error("preference unavailable"); return response.json(); })
    .then((current) => { decision = current; render(); })
    .catch(() => { decision = { decided: false, analytics: false, marketing: false }; error.textContent = "Privacy choices are temporarily unavailable. Optional tracking remains off."; render(); });
})();
