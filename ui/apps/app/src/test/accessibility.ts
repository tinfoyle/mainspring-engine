import axe from "axe-core";
import { expect } from "vitest";

function formatViolations(violations: axe.Result[]) {
  return violations.map((violation) => {
    const nodes = violation.nodes
      .map((node) => `  ${node.target.join(" ")} — ${node.failureSummary ?? violation.help}`)
      .join("\n");
    return `${violation.id}: ${violation.help}\n${nodes}`;
  }).join("\n\n");
}

export async function expectNoAxeViolations(root: Element) {
  const context = root.isConnected ? root : document.body.appendChild(root.cloneNode(true) as Element);
  try {
    const results = await axe.run(context, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
      rules: {
        // happy-dom does not calculate rendered pixel colors. Contrast remains
        // part of the real-browser release matrix.
        "color-contrast": { enabled: false }
      }
    });
    expect(results.violations, formatViolations(results.violations)).toEqual([]);
  } finally {
    if (context !== root) context.remove();
  }
}
