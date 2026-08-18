/** Cloudflare Worker entry point for the Infinite Ocean public website. */
import { handleImageOptimization, DEFAULT_DEVICE_SIZES, DEFAULT_IMAGE_SIZES } from "vinext/server/image-optimization";
import handler from "vinext/server/app-router-entry";

interface Env {
  ASSETS: Fetcher;
  DB: D1Database;
  SPYGLASS_ACCOUNT_API_ORIGIN?: string;
  IMAGES: {
    input(stream: ReadableStream): {
      transform(options: Record<string, unknown>): {
        output(options: { format: string; quality: number }): Promise<{ response(): Response }>;
      };
    };
  };
}

interface ExecutionContext {
  waitUntil(promise: Promise<unknown>): void;
  passThroughOnException(): void;
}

// Image security config. SVG sources with .svg extension auto-skip the
// optimization endpoint on the client side (served directly, no proxy).
// To route SVGs through the optimizer (with security headers), set
// dangerouslyAllowSVG: true in next.config.js and uncomment below:
// const imageConfig: ImageConfig = { dangerouslyAllowSVG: true };

const worker = {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/api/catalog") {
      if (request.method !== "GET") return new Response("Method not allowed", { status: 405, headers: { allow: "GET" } });
      let origin: URL;
      try {
        origin = new URL(env.SPYGLASS_ACCOUNT_API_ORIGIN ?? "");
      } catch {
        return new Response("Catalog unavailable", { status: 503 });
      }
      if (origin.protocol !== "https:" || origin.origin !== origin.href.replace(/\/$/, "") || origin.username || origin.password) {
        return new Response("Catalog unavailable", { status: 503 });
      }
      let upstream: Response;
      try {
        upstream = await fetch(new URL("/api/v1/catalog/public", origin), { headers: { accept: "application/json" }, signal: AbortSignal.timeout(5000) });
      } catch {
        return new Response("Catalog unavailable", { status: 502 });
      }
      if (!upstream.ok || !upstream.headers.get("content-type")?.toLowerCase().startsWith("application/json")) {
        return new Response("Catalog unavailable", { status: 502 });
      }
      return new Response(upstream.body, { status: 200, headers: { "content-type": "application/json; charset=utf-8", "cache-control": "public, max-age=60, stale-while-revalidate=300", "x-content-type-options": "nosniff" } });
    }

    if (url.pathname === "/_vinext/image") {
      const allowedWidths = [...DEFAULT_DEVICE_SIZES, ...DEFAULT_IMAGE_SIZES];
      return handleImageOptimization(request, {
        fetchAsset: (path) => env.ASSETS.fetch(new Request(new URL(path, request.url))),
        transformImage: async (body, { width, format, quality }) => {
          const result = await env.IMAGES.input(body).transform(width > 0 ? { width } : {}).output({ format, quality });
          return result.response();
        },
      }, allowedWidths);
    }

    return handler.fetch(request, env, ctx);
  },
};

export default worker;
