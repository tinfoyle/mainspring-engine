import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => new Map<string, { value: unknown }>());
vi.mock("#imports", () => ({
  useState: <T>(key: string, initialize: () => T) => {
    if (!state.has(key)) state.set(key, { value: initialize() });
    return state.get(key) as { value: T };
  }
}));

import { useAnalyticsConsent } from "../app/composables/useAnalyticsConsent";

beforeEach(() => state.clear());

describe("shared public analytics consent", () => {
  it("updates CTA measurement immediately after withdrawal and ignores an older response", () => {
    const consent = useAnalyticsConsent();
    consent.apply({ analytics: true, decided: true, marketing: false, policy_version: 2, renewal_required: false, surface: "public", effective_at: "2026-08-25T12:00:00Z" });
    expect(consent.allowed.value).toBe(true);

    consent.apply({ analytics: false, decided: true, marketing: false, policy_version: 2, renewal_required: false, surface: "public", effective_at: "2026-08-25T12:01:00Z" });
    expect(consent.allowed.value).toBe(false);

    consent.apply({ analytics: true, decided: true, marketing: false, policy_version: 2, renewal_required: false, surface: "public", effective_at: "2026-08-25T11:59:00Z" });
    expect(consent.allowed.value).toBe(false);
  });

  it("fails closed when consent cannot be read or must be renewed", () => {
    const consent = useAnalyticsConsent();
    consent.apply({ analytics: true, decided: true, marketing: false, policy_version: 1, renewal_required: true, surface: "public", effective_at: "2026-08-25T12:00:00Z" });
    expect(consent.allowed.value).toBe(false);
    consent.failClosed();
    expect(consent.allowed.value).toBe(false);
  });
});
