<script setup lang="ts">
import { erasePrivacyData, getPrivacyConsent, setPrivacyConsent, type PrivacyConsent } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { onMounted, ref } from "vue";

const preference = ref<PrivacyConsent>();
const analytics = ref(false);
const marketing = ref(false);
const saving = ref(false);
const message = ref("");

onMounted(async () => {
  preference.value = await getPrivacyConsent();
  analytics.value = preference.value.analytics;
  marketing.value = preference.value.marketing;
});

async function save(): Promise<void> {
  saving.value = true;
  try {
    preference.value = await setPrivacyConsent({ analytics: analytics.value, marketing: marketing.value });
    message.value = "Your privacy preferences were saved.";
  } finally { saving.value = false; }
}

async function erase(): Promise<void> {
  await erasePrivacyData();
  analytics.value = false;
  marketing.value = false;
  preference.value = undefined;
  message.value = "This browser's privacy receipt and analytics data were erased.";
}
</script>

<template>
  <section class="page privacy-page">
    <header class="page-heading"><p class="eyebrow">Your data</p><h1>Privacy choices</h1><p>Optional analytics never controls access to Spyglass, checkout, or Affiliate credit.</p></header>
    <form class="preference-panel" @submit.prevent="save">
      <div class="preference-row"><div><h2>Necessary</h2><p>Security, sign-in, checkout continuity and this preference.</p></div><strong>Always on</strong></div>
      <label class="preference-row"><span><strong>Analytics</strong><small>First-party, content-free journey and usability events.</small></span><input v-model="analytics" type="checkbox" /></label>
      <label class="preference-row"><span><strong>Marketing</strong><small>No marketing tracker is configured at launch.</small></span><input v-model="marketing" type="checkbox" /></label>
      <div class="preference-actions"><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : "Save preferences" }}</IoButton><IoButton kind="secondary" @click="analytics = false; marketing = false">Reject non-essential</IoButton></div>
    </form>
    <div class="privacy-danger"><h2>Erase browser privacy data</h2><p>Deletes this browser subject's consent receipts and raw analytics. It does not delete your Account, billing, or Affiliate records.</p><IoButton kind="quiet" @click="erase">Erase browser privacy data</IoButton></div>
    <p class="live-message" role="status" aria-live="polite">{{ message }}</p>
  </section>
</template>
