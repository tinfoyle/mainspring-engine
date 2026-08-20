import { exactRuntimeOrigin } from "@/lib/runtime-origin";

const maximumCatalogBytes = 256 * 1024;

function accountAPIOrigin(): string {
  const candidate = process.env.SPYGLASS_ACCOUNT_API_ORIGIN;
  if (!candidate) throw new Error("SPYGLASS_ACCOUNT_API_ORIGIN is required");
  return exactRuntimeOrigin(candidate, "SPYGLASS_ACCOUNT_API_ORIGIN", process.env.SPYGLASS_ENVIRONMENT);
}

export async function GET(): Promise<Response> {
  let origin: string;
  try {
    origin = accountAPIOrigin();
  } catch {
    return new Response("Catalog unavailable", { status: 503 });
  }

  let upstream: Response;
  try {
    upstream = await fetch(new URL("/api/v1/catalog/public", origin), {
      cache: "no-store",
      headers: { accept: "application/json" },
      redirect: "error",
      signal: AbortSignal.timeout(5000),
    });
  } catch {
    return new Response("Catalog unavailable", { status: 502 });
  }
  const contentType = upstream.headers.get("content-type")?.toLowerCase() ?? "";
  const length = Number(upstream.headers.get("content-length") ?? "0");
  if (!upstream.ok || !contentType.startsWith("application/json") || (Number.isFinite(length) && length > maximumCatalogBytes)) {
    return new Response("Catalog unavailable", { status: 502 });
  }
  const body = await upstream.arrayBuffer();
  if (body.byteLength > maximumCatalogBytes) return new Response("Catalog unavailable", { status: 502 });
  return new Response(body, {
    status: 200,
    headers: {
      "cache-control": "public, max-age=60, stale-while-revalidate=300",
      "content-type": "application/json; charset=utf-8",
    },
  });
}
