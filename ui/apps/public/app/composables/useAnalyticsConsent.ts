import { emitAnalytics, getPrivacyConsent, type AnalyticsEmission, type PrivacyConsent } from "@spyglass/api";
import { useState } from "#imports";

export function useAnalyticsConsent() {
  const allowed = useState<boolean>("analytics-consent-allowed", () => false);
  const effectiveAt = useState<string>("analytics-consent-effective-at", () => "");

  function apply(consent: PrivacyConsent): void {
    const nextEffectiveAt = consent.effective_at ?? "";
    const currentTime = Date.parse(effectiveAt.value);
    const nextTime = Date.parse(nextEffectiveAt);
    if (Number.isFinite(currentTime) && Number.isFinite(nextTime) && nextTime < currentTime) return;
    effectiveAt.value = nextEffectiveAt;
    allowed.value = consent.decided && consent.analytics && !consent.renewal_required;
  }

  function failClosed(): void {
    allowed.value = false;
  }

  async function resolve(): Promise<boolean> {
    try {
      apply(await getPrivacyConsent());
      return true;
    } catch {
      failClosed();
      return false;
    }
  }

  async function track(input: AnalyticsEmission): Promise<boolean> {
    try {
      return await emitAnalytics(allowed.value, input);
    } catch {
      return false;
    }
  }

  return { allowed, apply, failClosed, resolve, track };
}
