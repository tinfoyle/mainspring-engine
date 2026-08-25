import type { PublicCatalog } from "@spyglass/api";
import { useFetch, useState } from "#imports";
import { computed } from "vue";

export async function usePublicCatalog() {
  const state = useState<"fresh" | "stale" | "unavailable">("public-catalog-state", () => "fresh");
  const result = await useFetch<PublicCatalog>("/catalog.json", {
    key: "public-catalog",
    onResponse({ response }) {
      state.value = response.headers.get("x-spyglass-catalog-state") === "stale" ? "stale" : "fresh";
    },
    onResponseError() {
      state.value = "unavailable";
    }
  });
  const publishedLabel = computed(() => {
    const publishedAt = result.data.value?.published_at;
    if (!publishedAt) return "an earlier verified time";
    return `${new Intl.DateTimeFormat("en-US", { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(publishedAt))} UTC`;
  });
  return { ...result, state, publishedLabel };
}
