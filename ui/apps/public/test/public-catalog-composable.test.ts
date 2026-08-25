import { beforeEach, describe, expect, it, vi } from "vitest";

const harness = vi.hoisted(() => ({
  state: new Map<string, { value: unknown }>(),
  responseState: "fresh" as "fresh" | "stale",
  invokeHook: true,
  fail: false,
  catalog: { version: 7, published_at: "2026-08-25T12:00:00Z", packages: [], limits: [], plans: [], offers: [] }
}));

vi.mock("#imports", () => ({
  useState: <T>(key: string, initialize: () => T) => {
    if (!harness.state.has(key)) harness.state.set(key, { value: initialize() });
    return harness.state.get(key) as { value: T };
  },
  useFetch: async <T>(_path: string, options: {
    onResponse: (context: { response: { headers: Headers } }) => void;
    onResponseError: () => void;
  }) => {
    if (harness.fail) options.onResponseError();
    else if (harness.invokeHook) options.onResponse({ response: { headers: new Headers({ "x-spyglass-catalog-state": harness.responseState }) } });
    return {
      data: { value: harness.catalog as T },
      error: { value: harness.fail ? new Error("unavailable") : undefined },
      refresh: vi.fn()
    };
  }
}));

import { usePublicCatalog } from "../app/composables/usePublicCatalog";

beforeEach(() => {
  harness.state.clear();
  harness.responseState = "fresh";
  harness.invokeHook = true;
  harness.fail = false;
});

describe("shared public Catalog state", () => {
  it("retains the stale marker when Nuxt reuses its keyed payload across routes", async () => {
    harness.responseState = "stale";
    const first = await usePublicCatalog();
    expect(first.state.value).toBe("stale");

    harness.invokeHook = false;
    const reused = await usePublicCatalog();
    expect(reused.state.value).toBe("stale");
    expect(reused.publishedLabel.value).toContain("Aug 25, 2026");
  });

  it("marks retained data unavailable after a failed refresh", async () => {
    await usePublicCatalog();
    harness.fail = true;
    const failed = await usePublicCatalog();
    expect(failed.state.value).toBe("unavailable");
    expect(failed.error.value).toBeInstanceOf(Error);
  });
});
