<script setup lang="ts">
import { getPrivacyConsent, setPrivacyConsent, type PrivacyConsent } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { onMounted, ref } from "vue";

const preference = ref<PrivacyConsent>();
const ready = ref(false);
const managing = ref(false);
const analytics = ref(false);
const marketing = ref(false);
const saving = ref(false);
const error = ref("");

onMounted(async () => {
  try {
    preference.value = await getPrivacyConsent();
    analytics.value = preference.value.analytics;
    marketing.value = preference.value.marketing;
  } catch {
    error.value = "Privacy choices are temporarily unavailable. Optional tracking remains off.";
  } finally { ready.value = true; }
});

async function choose(nextAnalytics: boolean, nextMarketing = false): Promise<void> {
  saving.value = true;
  error.value = "";
  try {
    preference.value = await setPrivacyConsent({ analytics: nextAnalytics, marketing: nextMarketing });
    analytics.value = nextAnalytics;
    marketing.value = nextMarketing;
    managing.value = false;
  } catch {
    error.value = "We could not save that choice. Optional tracking remains off; please try again.";
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section v-if="ready && (!preference?.decided || managing)" class="consent" aria-labelledby="consent-title">
    <div class="consent__copy">
      <p class="eyebrow">Your choice</p>
      <h2 id="consent-title">Privacy without the fog</h2>
      <p>Necessary storage keeps the site secure. Optional first-party analytics helps us improve landing, checkout and onboarding—never your business content.</p>
    </div>
    <div v-if="managing" class="consent__options">
      <label><span><strong>Analytics</strong><small>Content-free journey events</small></span><input v-model="analytics" type="checkbox" /></label>
      <label><span><strong>Marketing</strong><small>Unused at launch</small></span><input v-model="marketing" type="checkbox" /></label>
    </div>
    <div class="consent__actions">
      <IoButton v-if="managing" :disabled="saving" @click="choose(analytics, marketing)">Save preferences</IoButton>
      <IoButton v-else :disabled="saving" @click="choose(true)">Accept analytics</IoButton>
      <IoButton kind="secondary" :disabled="saving" @click="choose(false)">Reject non-essential</IoButton>
      <IoButton v-if="!managing" kind="quiet" :disabled="saving" @click="managing = true">Manage preferences</IoButton>
    </div>
    <p v-if="error" class="consent__error" role="alert">{{ error }}</p>
  </section>
  <button v-else-if="ready" class="consent-reopen" type="button" @click="managing = true">Privacy choices</button>
</template>
