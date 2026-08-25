// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from "vitest";
import { installDialogFocus } from "./dialogFocus";

afterEach(() => { document.body.replaceChildren(); });

describe("dialog focus management", () => {
  it("announces a modal, contains Tab focus and restores its opener", async () => {
    const opener = document.createElement("button");
    const root = document.createElement("div");
    document.body.append(opener, root);
    opener.focus();
    const stop = installDialogFocus(root);
    const dialog = document.createElement("form");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    const first = document.createElement("button");
    const last = document.createElement("button");
    dialog.append(first, last);
    root.append(dialog);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(dialog.tabIndex).toBe(-1);
    expect(document.activeElement).toBe(dialog);
    last.focus();
    last.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
    expect(document.activeElement).toBe(first);
    first.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
    expect(document.activeElement).toBe(last);

    dialog.remove();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.activeElement).toBe(opener);
    stop();
  });
});
