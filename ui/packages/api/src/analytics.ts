import { requestJSON } from "./client";
import type { AnalyticsEventName } from "./generated/api-types";

export interface AnalyticsEmission {
  name: AnalyticsEventName;
  fields?: Readonly<Record<string, string>>;
}

export async function emitAnalytics(consented: boolean, input: AnalyticsEmission): Promise<boolean> {
  if (!consented) return false;
  await requestJSON<void>("/api/v1/analytics/events", {
    method: "POST",
    body: JSON.stringify({
      event_id: crypto.randomUUID(),
      name: input.name,
      occurred_at: new Date().toISOString(),
      fields: input.fields ?? {}
    })
  });
  return true;
}
