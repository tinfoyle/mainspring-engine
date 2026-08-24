import type { PublicCatalog } from "@spyglass/api";

export default defineEventHandler(async (event): Promise<PublicCatalog> => {
  const config = useRuntimeConfig(event);
  setResponseHeader(event, "Cache-Control", "public, max-age=60, stale-if-error=300");
  return $fetch<PublicCatalog>("/api/v1/catalog/public", { baseURL: config.accountAPIOrigin });
});
