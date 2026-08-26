<script setup lang="ts">
import {
  APIProblem,
  cancelPrivacyRightsRequest,
  erasePrivacyData,
  getPrivacyConsent,
  getPrivacyConsentHistory,
  listPrivacyRightsRequests,
  setPrivacyConsent,
  submitPrivacyRightsRequest,
  type PrivacyConsent,
  type PrivacyDecision,
  type PrivacyRightsKind,
  type PrivacyRightsRequest,
  type PrivacyRightsScope
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";

const route = useRoute();
const preference = ref<PrivacyConsent>();
const consentHistory = ref<ReadonlyArray<PrivacyDecision>>([]);
const analytics = ref(false);
const marketing = ref(false);
const requests = ref<ReadonlyArray<PrivacyRightsRequest>>([]);
const kind = ref<PrivacyRightsKind>("access");
const scope = ref<PrivacyRightsScope>("identity");
const loading = ref(true);
const saving = ref(false);
const submitting = ref(false);
const cancelingID = ref("");
const confirmingBrowserErase = ref(false);
const erasingBrowserData = ref(false);
const message = ref("");
const errorMessage = ref("");
const historyError = ref("");
const navigationNotice = ref("");

const openEquivalent = computed(() => requests.value.some((request) =>
  request.kind === kind.value && request.scope === scope.value && ["submitted", "in_review"].includes(request.state)));
const privacyDirty = computed(() => confirmingBrowserErase.value
  || analytics.value !== (preference.value?.analytics ?? false)
  || marketing.value !== (preference.value?.marketing ?? false));
const privacyPending = computed(() => saving.value || submitting.value || erasingBrowserData.value || Boolean(cancelingID.value));
const { allowNextNavigation } = useSafeNavigation({
  dirty: privacyDirty,
  pending: privacyPending,
  message: "Leave Privacy? Your unsaved consent choice or browser-erasure confirmation will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Privacy request is still in progress. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Privacy choices remain available.";
  }
});

const scopeDescriptions: Record<PrivacyRightsScope, string> = {
  identity: "Your login identity, verified contact, authentication records and identity-wide account relationships.",
  account: "Data governed by one or more Spyglass Accounts. Owners can also use the dedicated export and lifecycle tools below.",
  affiliate: "Your Affiliate enrollment, generated code, attribution aggregates and commission ledger. Legal or accounting retention may limit erasure.",
  analytics: "First-party analytics associated with identifiable private-app subjects. This browser's current subject can be erased immediately below."
};

onMounted(async () => {
  loading.value = true;
  const [consentResult, rightsResult] = await Promise.allSettled([getPrivacyConsent(), listPrivacyRightsRequests()]);
  if (consentResult.status === "fulfilled") {
    preference.value = consentResult.value;
    analytics.value = consentResult.value.analytics;
    marketing.value = consentResult.value.marketing;
  } else {
    errorMessage.value = "Privacy preferences are temporarily unavailable. Optional tracking remains off unless an existing valid choice allows it.";
  }
  if (rightsResult.status === "fulfilled") requests.value = rightsResult.value.requests;
  else errorMessage.value ||= "Privacy rights request history is temporarily unavailable.";
  await refreshConsentHistory();
  loading.value = false;
});

async function refreshConsentHistory(): Promise<void> {
  historyError.value = "";
  try {
    consentHistory.value = (await getPrivacyConsentHistory()).decisions;
  } catch {
    historyError.value = "Consent history is temporarily unavailable. Your current preference still applies.";
  }
}

async function save(): Promise<void> {
  if (saving.value) return;
  saving.value = true;
  message.value = "";
  errorMessage.value = "";
  navigationNotice.value = "";
  const previous = preference.value;
  try {
    preference.value = await setPrivacyConsent({ analytics: analytics.value, marketing: marketing.value });
    analytics.value = preference.value.analytics;
    marketing.value = preference.value.marketing;
    message.value = "Your privacy preferences were saved.";
    void refreshConsentHistory();
  } catch (error) {
    analytics.value = previous?.analytics ?? false;
    marketing.value = previous?.marketing ?? false;
    errorMessage.value = error instanceof APIProblem ? error.message : "Privacy preferences could not be saved.";
  } finally { saving.value = false; }
}

async function rejectNonEssential(): Promise<void> {
  if (saving.value) return;
  analytics.value = false;
  marketing.value = false;
  await save();
}

async function eraseBrowserSubject(): Promise<void> {
  if (erasingBrowserData.value) return;
  if (!confirmingBrowserErase.value) {
    confirmingBrowserErase.value = true;
    return;
  }
  errorMessage.value = "";
  message.value = "";
  erasingBrowserData.value = true;
  try {
    await erasePrivacyData();
    analytics.value = false;
    marketing.value = false;
    preference.value = undefined;
    consentHistory.value = [];
    historyError.value = "";
    confirmingBrowserErase.value = false;
    message.value = "This browser's privacy receipt and raw analytics were erased.";
  } catch (error) {
    errorMessage.value = error instanceof APIProblem ? error.message : "This browser's privacy data could not be erased.";
  } finally {
    erasingBrowserData.value = false;
  }
}

function requireStrongAuthentication(): void {
  allowNextNavigation(); window.location.assign(`/app/security?return_to=${encodeURIComponent(route.fullPath)}&status=strong_reauthentication_required`);
}

async function submitRightsRequest(): Promise<void> {
  if (submitting.value || openEquivalent.value) return;
  submitting.value = true;
  message.value = "";
  errorMessage.value = "";
  try {
    const created = await submitPrivacyRightsRequest({ kind: kind.value, scope: scope.value });
    requests.value = [created, ...requests.value];
    message.value = `Your ${label(kind.value)} request for ${label(scope.value)} data was received.`;
  } catch (error) {
    if (error instanceof APIProblem && error.problem?.code === "strong_reauthentication_required") {
      requireStrongAuthentication();
      return;
    }
    errorMessage.value = error instanceof APIProblem ? error.message : "Your privacy rights request could not be submitted.";
  } finally { submitting.value = false; }
}

async function cancelRequest(requestID: string): Promise<void> {
  if (cancelingID.value) return;
  cancelingID.value = requestID;
  message.value = "";
  errorMessage.value = "";
  try {
    const canceled = await cancelPrivacyRightsRequest(requestID);
    requests.value = requests.value.map((request) => request.request_id === requestID ? canceled : request);
    message.value = "The submitted request was canceled.";
  } catch (error) {
    if (error instanceof APIProblem && error.problem?.code === "strong_reauthentication_required") {
      requireStrongAuthentication();
      return;
    }
    errorMessage.value = error instanceof APIProblem ? error.message : "The request could not be canceled.";
  } finally { cancelingID.value = ""; }
}

function label(value: string): string {
  return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase());
}

function date(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value));
}
</script>

<template>
  <section class="page privacy-page">
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading">
      <p class="eyebrow">Your data</p>
      <h1>Privacy you can act on.</h1>
      <p>Control optional measurement, erase this browser's pseudonymous data, or submit a verified request covering your identity, Accounts, analytics, or Affiliate records.</p>
    </header>

    <div v-if="loading" class="queue-state" role="status">Loading your privacy controls…</div>
    <template v-else>
      <form class="preference-panel" @submit.prevent="save">
        <div class="privacy-section-heading"><div><p class="eyebrow">Consent</p><h2>Optional measurement</h2></div><span>Policy {{ preference?.policy_version ?? 1 }}</span></div>
        <div class="preference-row"><div><h3>Necessary</h3><p>Security, sign-in, checkout continuity and this preference.</p></div><strong>Always on</strong></div>
        <label class="preference-row"><span><strong>Analytics</strong><small>First-party, content-free journey and usability events.</small></span><input v-model="analytics" type="checkbox" /></label>
        <label class="preference-row"><span><strong>Marketing</strong><small>No marketing tracker or processor is configured at launch.</small></span><input v-model="marketing" type="checkbox" /></label>
        <div class="preference-actions"><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : "Save preferences" }}</IoButton><IoButton kind="secondary" :disabled="saving" @click="rejectNonEssential">{{ saving ? "Saving…" : "Reject non-essential" }}</IoButton></div>
        <section class="rights-history consent-history" aria-labelledby="consent-history-heading">
          <h3 id="consent-history-heading">Consent history</h3>
          <p class="form-note">Immutable optional-purpose choices saved for this browser are shown newest first. Necessary processing is always listed separately above.</p>
          <p v-if="historyError" class="form-error" role="alert">{{ historyError }}</p>
          <p v-else-if="consentHistory.length === 0" class="form-note">No saved consent decision exists for this browser.</p>
          <ol v-else>
            <li v-for="decision in consentHistory" :key="decision.decision_id">
              <div><strong>{{ date(decision.effective_at) }}</strong><small>Policy {{ decision.policy_version }} · {{ label(decision.surface) }} surface</small></div>
              <div class="consent-decision"><span>Analytics {{ decision.analytics ? "accepted" : "rejected" }}</span><span>Marketing {{ decision.marketing ? "accepted" : "rejected" }}</span></div>
            </li>
          </ol>
        </section>
      </form>

      <section class="rights-panel" aria-labelledby="rights-heading">
        <div class="privacy-section-heading"><div><p class="eyebrow">GDPR rights</p><h2 id="rights-heading">Make a tracked request</h2></div><span>Passkey required</span></div>
        <p class="section-intro">Choose a right and the data boundary it concerns. Submission and cancellation require a recent passkey confirmation. We track the response deadline; submitting a request does not silently delete legally retained billing or Affiliate evidence.</p>
        <form class="rights-form" @submit.prevent="submitRightsRequest">
          <label for="rights-kind">What would you like to do?</label>
          <select id="rights-kind" v-model="kind">
            <option value="access">Access my data</option><option value="portability">Receive portable data</option><option value="correction">Correct my data</option><option value="erasure">Erase eligible data</option><option value="restriction">Restrict processing</option><option value="objection">Object to processing</option>
          </select>
          <label for="rights-scope">Which records?</label>
          <select id="rights-scope" v-model="scope">
            <option value="identity">Identity</option><option value="account">Account</option><option value="affiliate">Affiliate</option><option value="analytics">Analytics</option>
          </select>
          <p class="scope-description">{{ scopeDescriptions[scope] }}</p>
          <p v-if="openEquivalent" class="queue-inline-status">An equivalent request is already open. Its current state is shown below.</p>
          <IoButton type="submit" :disabled="submitting || openEquivalent">{{ submitting ? "Submitting…" : "Submit verified request" }}</IoButton>
        </form>

        <div class="rights-history">
          <h3>Request history</h3>
          <p v-if="requests.length === 0" class="form-note">No privacy rights requests have been submitted.</p>
          <ol v-else>
            <li v-for="request in requests" :key="request.request_id">
              <div><strong>{{ label(request.kind) }} · {{ label(request.scope) }}</strong><small>Submitted {{ date(request.requested_at) }} · response due {{ date(request.response_due_at) }}</small></div>
              <div class="rights-state"><span :data-state="request.state">{{ label(request.state) }}</span><IoButton v-if="request.state === 'submitted'" kind="quiet" :disabled="Boolean(cancelingID)" @click="cancelRequest(request.request_id)">{{ cancelingID === request.request_id ? "Canceling…" : "Cancel" }}</IoButton></div>
            </li>
          </ol>
        </div>
      </section>

      <section class="privacy-tools" aria-labelledby="privacy-tools-heading">
        <div class="privacy-section-heading"><div><p class="eyebrow">Direct tools</p><h2 id="privacy-tools-heading">Use the narrowest control</h2></div></div>
        <div class="privacy-tool-grid">
          <article><h3>Account portability</h3><p>Owners can build and download a governed ZIP snapshot of the selected Account.</p><a href="/app/account-exports">Open Account exports</a></article>
          <article><h3>Account lifecycle</h3><p>Owners can freeze and close an Account through its audited cooling-off workflow.</p><a href="/app/account-closures">Open Account lifecycle</a></article>
          <article><h3>Identity correction</h3><p>Change the verified login email or review authentication and active sessions.</p><a href="/app/security">Open identity security</a></article>
        </div>
      </section>

      <section class="privacy-danger" aria-labelledby="browser-erasure-heading">
        <h2 id="browser-erasure-heading">Erase this browser's privacy data</h2>
        <p>Immediately deletes the current host-only privacy subject's consent receipts and raw analytics. It does not delete your login, Account, billing, or Affiliate records.</p>
        <div v-if="confirmingBrowserErase" class="queue-inline-status queue-inline-status--error">This action signs this browser out of its saved privacy choice. Optional tracking remains off until you choose again.</div>
        <div class="preference-actions"><IoButton kind="quiet" :disabled="erasingBrowserData" @click="eraseBrowserSubject">{{ erasingBrowserData ? "Erasing browser data…" : confirmingBrowserErase ? "Confirm browser-data erasure" : "Erase browser privacy data" }}</IoButton><IoButton v-if="confirmingBrowserErase" kind="secondary" :disabled="erasingBrowserData" @click="confirmingBrowserErase = false">Keep browser data</IoButton></div>
      </section>
    </template>
    <p v-if="errorMessage" class="form-error privacy-feedback" role="alert">{{ errorMessage }}</p>
    <p class="live-message privacy-feedback" role="status" aria-live="polite">{{ message }}</p>
  </section>
</template>
