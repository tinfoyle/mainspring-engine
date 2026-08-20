export const dynamic = "force-static";

export function GET(): Response {
  return Response.json({ status: "ready" }, { headers: { "cache-control": "no-store" } });
}
