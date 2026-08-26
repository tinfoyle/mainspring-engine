<script setup lang="ts">
import { getPrivacyConsent, setPrivacyConsent, type PrivacyConsent } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";

const preference = ref<PrivacyConsent>();
const ready = ref(false);
const managing = ref(false);
const analytics = ref(false);
const saving = ref(false);
const error = ref("");
const consentPanel = ref<HTMLElement>();
const reopenButton = ref<HTMLButtonElement>();
const consentState = useAnalyticsConsent();

onMounted(async () => {
  window.addEventListener("spyglass:privacy-data-erased", handlePrivacyErased);
  try {
    preference.value = await getPrivacyConsent();
    consentState.apply(preference.value);
    analytics.value = preference.value.analytics;
  } catch {
    consentState.failClosed();
    error.value = "Privacy choices are temporarily unavailable. Optional tracking remains off.";
  } finally { ready.value = true; }
});

onUnmounted(() => window.removeEventListener("spyglass:privacy-data-erased", handlePrivacyErased));

function handlePrivacyErased(): void {
  preference.value = undefined;
  analytics.value = false;
  managing.value = false;
  error.value = "";
  consentState.failClosed();
}

async function choose(nextAnalytics: boolean): Promise<void> {
  const previous = preference.value;
  saving.value = true;
  error.value = "";
  try {
    preference.value = await setPrivacyConsent({ analytics: nextAnalytics, marketing: false });
    consentState.apply(preference.value);
    analytics.value = preference.value.analytics;
    managing.value = false;
    window.dispatchEvent(new Event("spyglass:privacy-choice-saved"));
    await nextTick();
    reopenButton.value?.focus();
  } catch {
    analytics.value = previous?.analytics ?? false;
    if (previous) consentState.apply(previous);
    else consentState.failClosed();
    error.value = previous?.decided && !previous.renewal_required
      ? "We could not save that change. Your previous choice remains in effect; please try again."
      : "We could not save that choice. Optional tracking remains off; please try again.";
  } finally {
    saving.value = false;
  }
}

async function openPreferences(): Promise<void> {
  managing.value = true;
  await nextTick();
  consentPanel.value?.querySelector<HTMLElement>("input")?.focus();
}
</script>

<template>
  <section v-if="ready && (!preference?.decided || preference.renewal_required || managing)" ref="consentPanel" class="consent" aria-labelledby="consent-title">
    <div class="consent__copy">
      <p class="eyebrow">Your choice</p>
      <h2 id="consent-title">Privacy without the fog</h2>
      <p>Necessary storage keeps the site secure. Optional, content-free analytics improves landing, checkout and onboarding—never your business content.</p>
    </div>
    <div v-if="managing" class="consent__options">
      <label><span><strong>Analytics</strong><small>Content-free journey events</small></span><input v-model="analytics" type="checkbox" /></label>
      <p><strong>Marketing tracking is not used.</strong> No marketing purpose, processor, cookie or destination is configured, so Spyglass does not ask you to consent to one.</p>
      <a class="consent__history-link" href="/privacy#consent-history">View this browser's consent history</a>
    </div>
    <div class="consent__actions">
      <IoButton v-if="managing" :disabled="saving" @click="choose(analytics)">Save preferences</IoButton>
      <IoButton v-else kind="secondary" :disabled="saving" @click="choose(true)">Accept analytics</IoButton>
      <IoButton kind="secondary" :disabled="saving" @click="choose(false)">Reject non-essential</IoButton>
      <IoButton v-if="!managing" kind="quiet" :disabled="saving" @click="openPreferences">Manage preferences</IoButton>
    </div>
    <p v-if="error" class="consent__error" role="alert">{{ error }}</p>
  </section>
  <button v-else-if="ready" ref="reopenButton" class="consent-reopen" type="button" @click="openPreferences">Privacy choices</button>
</template>
