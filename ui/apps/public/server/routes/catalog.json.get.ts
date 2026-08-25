import type { PublicCatalog } from "@spyglass/api";
import { createError, defineEventHandler, setResponseHeader } from "h3";
import { useRuntimeConfig } from "#imports";
import { PublicCatalogCache, resolvePublicCatalog } from "../utils/publicCatalogCache";

const publicCatalogCache = new PublicCatalogCache(300_000);

export default defineEventHandler(async (event): Promise<PublicCatalog> => {
  const config = useRuntimeConfig(event);
  try {
    const projection = await resolvePublicCatalog(
      () => $fetch<PublicCatalog>("/api/v1/catalog/public", { baseURL: config.accountAPIOrigin, timeout: 2_000 }),
      publicCatalogCache
    );
    setResponseHeader(event, "X-Spyglass-Catalog-State", projection.state);
    setResponseHeader(event, "X-Spyglass-Catalog-Age", String(projection.ageSeconds));
    if (projection.state === "fresh") {
      setResponseHeader(event, "Cache-Control", "public, max-age=60, stale-if-error=300");
    } else {
      setResponseHeader(event, "Cache-Control", "public, max-age=15, must-revalidate");
      setResponseHeader(event, "Warning", '110 - "Response is stale"');
    }
    return projection.catalog;
  } catch {
    setResponseHeader(event, "Cache-Control", "no-store");
    setResponseHeader(event, "Retry-After", 30);
    setResponseHeader(event, "X-Spyglass-Catalog-State", "unavailable");
    throw createError({ statusCode: 503, statusMessage: "Published Catalog temporarily unavailable" });
  }
});
