<script setup lang="ts">
import { erasePrivacyData, getPrivacyConsentHistory, type PrivacyDecision } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { onMounted, onUnmounted, ref } from "vue";

const decisions = ref<ReadonlyArray<PrivacyDecision>>([]);
const loading = ref(true);
const confirmingErase = ref(false);
const erasing = ref(false);
const error = ref("");
const message = ref("");
let requestSequence = 0;

onMounted(() => {
  window.addEventListener("spyglass:privacy-choice-saved", refreshAfterChoice);
  void loadHistory();
});

onUnmounted(() => window.removeEventListener("spyglass:privacy-choice-saved", refreshAfterChoice));

function refreshAfterChoice(): void {
  void loadHistory();
}

async function loadHistory(): Promise<void> {
  const sequence = ++requestSequence;
  loading.value = true;
  error.value = "";
  try {
    const history = await getPrivacyConsentHistory();
    if (sequence === requestSequence) decisions.value = history.decisions;
  } catch {
    if (sequence === requestSequence) error.value = "Consent history is temporarily unavailable. Optional tracking remains off unless an existing valid choice permits it.";
  } finally {
    if (sequence === requestSequence) loading.value = false;
  }
}

async function eraseBrowserData(): Promise<void> {
  if (erasing.value) return;
  if (!confirmingErase.value) {
    confirmingErase.value = true;
    message.value = "";
    return;
  }
  erasing.value = true;
  error.value = "";
  message.value = "";
  try {
    await erasePrivacyData();
    requestSequence += 1;
    decisions.value = [];
    loading.value = false;
    confirmingErase.value = false;
    message.value = "This browser's public-site consent receipts and raw analytics were erased. Optional tracking remains off until you choose again.";
    window.dispatchEvent(new Event("spyglass:privacy-data-erased"));
  } catch {
    error.value = "This browser's public-site privacy data could not be erased.";
  } finally {
    erasing.value = false;
  }
}

function date(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
</script>

<template>
  <section id="consent-history" class="public-consent-history" aria-labelledby="public-consent-history-heading">
    <h2 id="public-consent-history-heading">This browser's consent history</h2>
    <p>Saved optional-purpose choices for this public-site browser are shown newest first. Internal receipt and subject identifiers are never displayed.</p>
    <p v-if="loading" class="public-consent-history__status" role="status">Loading consent history…</p>
    <p v-else-if="error" class="public-consent-history__error" role="alert">{{ error }}</p>
    <p v-else-if="decisions.length === 0" class="public-consent-history__status">No saved public-site consent decision exists for this browser.</p>
    <ol v-else>
      <li v-for="decision in decisions" :key="decision.decision_id">
        <div><strong>{{ date(decision.effective_at) }}</strong><small>Policy {{ decision.policy_version }}</small></div>
        <div><span>Analytics {{ decision.analytics ? "accepted" : "rejected" }}</span><span>Marketing {{ decision.marketing ? "accepted" : "rejected" }}</span></div>
      </li>
    </ol>
    <div v-if="confirmingErase" class="public-consent-history__warning"><strong>Erase this browser's public-site privacy data?</strong><span>The saved preference and raw analytics will be deleted. Your login, Accounts, billing and Affiliate records are not affected.</span></div>
    <div class="public-consent-history__actions">
      <IoButton kind="quiet" :disabled="erasing" @click="eraseBrowserData">{{ erasing ? "Erasing browser data…" : confirmingErase ? "Confirm public-site data erasure" : "Erase this browser's privacy data" }}</IoButton>
      <IoButton v-if="confirmingErase" kind="secondary" :disabled="erasing" @click="confirmingErase = false">Keep browser data</IoButton>
    </div>
    <p class="public-consent-history__message" role="status" aria-live="polite">{{ message }}</p>
  </section>
</template>
