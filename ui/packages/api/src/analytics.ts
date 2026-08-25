import type { AnalyticsEventName } from "./generated/api-types";

export interface AnalyticsEmission {
  name: AnalyticsEventName;
  fields?: Readonly<Record<string, string>>;
}

export async function emitAnalytics(consented: boolean, input: AnalyticsEmission): Promise<boolean> {
  if (!consented) return false;
  const response = await fetch("/api/v1/analytics/events", {
    method: "POST",
    credentials: "same-origin",
    keepalive: true,
    headers: { "Accept": "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({
      event_id: crypto.randomUUID(),
      name: input.name,
      occurred_at: new Date().toISOString(),
      fields: input.fields ?? {}
    })
  });
  return response.ok;
}
