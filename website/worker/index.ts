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

const HTML_CONTENT_SECURITY_POLICY = [
  "default-src 'self'",
  "base-uri 'none'",
  "object-src 'none'",
  "frame-ancestors 'none'",
  "form-action 'self'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "upgrade-insecure-requests",
].join("; ");

function securePublicResponse(request: Request, response: Response): Response {
  const headers = new Headers(response.headers);
  headers.set("x-content-type-options", "nosniff");
  headers.set("referrer-policy", "strict-origin-when-cross-origin");
  headers.set("permissions-policy", "camera=(), geolocation=(), microphone=(), payment=(), usb=()");
  headers.set("cross-origin-opener-policy", "same-origin");
  headers.set("cross-origin-resource-policy", "same-origin");
  headers.set("x-frame-options", "DENY");
  if (new URL(request.url).protocol === "https:") {
    headers.set("strict-transport-security", "max-age=31536000; includeSubDomains");
  }
  if (headers.get("content-type")?.toLowerCase().startsWith("text/html")) {
    headers.set("content-security-policy", HTML_CONTENT_SECURITY_POLICY);
  }
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
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
      if (request.method !== "GET") return securePublicResponse(request, new Response("Method not allowed", { status: 405, headers: { allow: "GET" } }));
      let origin: URL;
      try {
        origin = new URL(env.SPYGLASS_ACCOUNT_API_ORIGIN ?? "");
      } catch {
        return securePublicResponse(request, new Response("Catalog unavailable", { status: 503 }));
      }
      if (origin.protocol !== "https:" || origin.origin !== origin.href.replace(/\/$/, "") || origin.username || origin.password) {
        return securePublicResponse(request, new Response("Catalog unavailable", { status: 503 }));
      }
      let upstream: Response;
      try {
        upstream = await fetch(new URL("/api/v1/catalog/public", origin), { headers: { accept: "application/json" }, signal: AbortSignal.timeout(5000) });
      } catch {
        return securePublicResponse(request, new Response("Catalog unavailable", { status: 502 }));
      }
      if (!upstream.ok || !upstream.headers.get("content-type")?.toLowerCase().startsWith("application/json")) {
        return securePublicResponse(request, new Response("Catalog unavailable", { status: 502 }));
      }
      return securePublicResponse(request, new Response(upstream.body, { status: 200, headers: { "content-type": "application/json; charset=utf-8", "cache-control": "public, max-age=60, stale-while-revalidate=300" } }));
    }

    if (url.pathname === "/_vinext/image") {
      const allowedWidths = [...DEFAULT_DEVICE_SIZES, ...DEFAULT_IMAGE_SIZES];
      const response = await handleImageOptimization(request, {
        fetchAsset: (path) => env.ASSETS.fetch(new Request(new URL(path, request.url))),
        transformImage: async (body, { width, format, quality }) => {
          const result = await env.IMAGES.input(body).transform(width > 0 ? { width } : {}).output({ format, quality });
          return result.response();
        },
      }, allowedWidths);
      return securePublicResponse(request, response);
    }

    return securePublicResponse(request, await handler.fetch(request, env, ctx));
  },
};

export default worker;
