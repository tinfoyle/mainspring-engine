const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])'
].join(",");

interface DialogState {
  previous: HTMLElement | undefined;
  keydown: (event: KeyboardEvent) => void;
}

function focusableChildren(dialog: HTMLElement): HTMLElement[] {
  return Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector))
    .filter((item) => !item.hidden && item.getAttribute("aria-hidden") !== "true");
}

export function installDialogFocus(root: HTMLElement): () => void {
  const states = new Map<HTMLElement, DialogState>();

  function manage(dialog: HTMLElement): void {
    if (states.has(dialog)) return;
    const active = document.activeElement;
    const state: DialogState = {
      previous: active instanceof HTMLElement ? active : undefined,
      keydown: (event) => {
        if (event.key !== "Tab") return;
        const focusable = focusableChildren(dialog);
        const first = focusable[0];
        const last = focusable.at(-1);
        if (!first || !last) {
          event.preventDefault();
          dialog.focus();
          return;
        }
        if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog)) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    states.set(dialog, state);
    if (!dialog.hasAttribute("tabindex")) dialog.tabIndex = -1;
    dialog.addEventListener("keydown", state.keydown);
    queueMicrotask(() => dialog.isConnected && dialog.focus());
  }

  function release(dialog: HTMLElement, restore: boolean): void {
    const state = states.get(dialog);
    if (!state) return;
    dialog.removeEventListener("keydown", state.keydown);
    states.delete(dialog);
    if (restore && state.previous?.isConnected) queueMicrotask(() => state.previous?.focus());
  }

  function dialogs(node: Node): HTMLElement[] {
    if (!(node instanceof HTMLElement)) return [];
    const matches = node.matches('[role="dialog"][aria-modal="true"]') ? [node] : [];
    return matches.concat(Array.from(node.querySelectorAll<HTMLElement>('[role="dialog"][aria-modal="true"]')));
  }

  root.querySelectorAll<HTMLElement>('[role="dialog"][aria-modal="true"]').forEach(manage);
  const observer = new MutationObserver((records) => {
    for (const record of records) {
      record.removedNodes.forEach((node) => dialogs(node).forEach((dialog) => release(dialog, true)));
      record.addedNodes.forEach((node) => dialogs(node).forEach(manage));
    }
  });
  observer.observe(root, { childList: true, subtree: true });

  return () => {
    observer.disconnect();
    states.forEach((_state, dialog) => release(dialog, false));
  };
}
