(() => {
  const scrollTranscriptToLatest = (root = document) => {
    const transcript = root.querySelector?.("[data-baseline-transcript]");
    if (!transcript) return;

    // Wait until layout and fonts have established the final scroll height.
    window.requestAnimationFrame(() => {
      transcript.scrollTop = transcript.scrollHeight;
    });
  };

  const enableEnterToSend = (root = document) => {
    const composer = root.querySelector?.(".baseline-answer-form textarea[name='answer']");
    if (!composer || composer.dataset.enterToSend === "true") return;

    composer.dataset.enterToSend = "true";
    composer.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || event.shiftKey || event.isComposing) return;

      event.preventDefault();
      composer.form?.requestSubmit();
    });
  };

  const initializeBaselineChat = (root = document) => {
    scrollTranscriptToLatest(root);
    enableEnterToSend(root);
  };

  const focusEvidenceAnchor = () => {
    if (!window.location.hash.startsWith("#evidence-interview-")) return;
    window.requestAnimationFrame(() => {
      const target = document.querySelector(window.location.hash);
      target?.scrollIntoView({ block: "center", behavior: "instant" });
      target?.focus?.({ preventScroll: true });
    });
  };

  document.addEventListener("DOMContentLoaded", () => {
    initializeBaselineChat();
    focusEvidenceAnchor();
  });
  document.addEventListener("htmx:afterSettle", (event) => initializeBaselineChat(event.target));
  window.addEventListener("pageshow", () => {
    initializeBaselineChat();
    focusEvidenceAnchor();
  });
})();
