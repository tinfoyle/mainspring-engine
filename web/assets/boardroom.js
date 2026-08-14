(() => {
  function deduplicateMessages(root = document) {
    const seen = new Set();
    root.querySelectorAll("[data-message-id]").forEach((message) => {
      const id = message.getAttribute("data-message-id");
      if (!id) return;
      if (seen.has(id)) {
        message.remove();
        return;
      }
      seen.add(id);
    });
  }

  function start() {
    deduplicateMessages();
    const messages = document.getElementById("messages");
    if (!messages) return;
    new MutationObserver(() => deduplicateMessages(messages)).observe(messages, { childList: true });
    document.body.addEventListener("htmx:afterSwap", () => deduplicateMessages(messages));
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
