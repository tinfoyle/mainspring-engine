<script setup lang="ts">
import {
  APIProblem,
  createCheckoutSession,
  emitAnalytics,
  getBillingStatus,
  getPrivacyConsent,
  getPublicCatalog,
  type BillingStatus,
  type CatalogOffer,
  type CatalogPlan,
  type PublicCatalog
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { useSessionStore } from "../stores/session";

const referralPattern = /^[A-Za-z0-9][A-Za-z0-9-]{4,22}[A-Za-z0-9]$/;
const route = useRoute();
const session = useSessionStore();
const catalog = ref<PublicCatalog>();
const billing = ref<BillingStatus>();
const loading = ref(true);
const submitting = ref(false);
const errorMessage = ref("");
const referralInput = ref("");
const appliedReferral = ref("");
const referralError = ref("");
const referralEntryMethod = ref<"manual" | "link">("manual");
const confirmed = ref(false);
const requestID = ref("");
const analyticsAllowed = ref(false);
const returnState = ref<"none" | "cancelled" | "pending" | "projected">("none");
let pollTimer: number | undefined;
let pollCount = 0;

const paidOffers = computed(() => (catalog.value?.offers ?? []).filter((offer) =>
  offer.amount_minor > 0 && offer.billing_interval !== "none" && new Date(offer.effective_from).getTime() <= Date.now()
));
const selectedOfferCode = ref("");
const selectedOffer = computed(() => paidOffers.value.find((offer) => offer.code === selectedOfferCode.value));
const selectedPlan = computed<CatalogPlan | undefined>(() => {
  const offer = selectedOffer.value;
  return catalog.value?.plans.find((plan) => plan.code === offer?.plan_code && plan.version === offer.plan_version);
});
const canManage = computed(() => session.selected?.role === "owner" || session.selected?.role === "billing_admin");
const canCheckout = computed(() => canManage.value && billing.value?.can_start_checkout === true);
const referralApplied = computed(() => appliedReferral.value !== "");

function formatPrice(offer: CatalogOffer): string {
  return new Intl.NumberFormat(undefined, { style: "currency", currency: offer.currency }).format(offer.amount_minor / 100);
}

function normalizeReferral(value: string): string {
  return value.trim().toUpperCase();
}

function applyReferral(): void {
  const normalized = normalizeReferral(referralInput.value);
  if (!referralPattern.test(normalized)) {
    referralError.value = "Enter the complete Affiliate code, including any hyphens.";
    return;
  }
  appliedReferral.value = normalized;
  referralInput.value = normalized;
  referralError.value = "";
  confirmed.value = false;
  requestID.value = "";
}

function removeReferral(): void {
  referralInput.value = "";
  appliedReferral.value = "";
  referralError.value = "";
  confirmed.value = false;
  requestID.value = "";
}

async function emit(name: Parameters<typeof emitAnalytics>[1]["name"], fields: Readonly<Record<string, string>>): Promise<void> {
  try {
    await emitAnalytics(analyticsAllowed.value, { name, fields });
  } catch {
    // Optional measurement never changes checkout behavior.
  }
}

async function loadBilling(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID) return;
  try {
    billing.value = await getBillingStatus(accountID);
    errorMessage.value = "";
  } catch (error) {
    errorMessage.value = error instanceof APIProblem ? error.message : "Billing details are temporarily unavailable.";
  }
}

function chooseOffer(): void {
  const requested = typeof route.query.offer === "string" ? route.query.offer : "";
  selectedOfferCode.value = paidOffers.value.some((offer) => offer.code === requested)
    ? requested
    : paidOffers.value[0]?.code ?? "";
}

function evaluateReturn(): void {
  const status = typeof route.query.status === "string" ? route.query.status : "";
  if (status === "billing_cancelled") {
    returnState.value = "cancelled";
    void emit("checkout_returned", { offer_code: selectedOfferCode.value, result: "cancelled" });
    return;
  }
  if (status !== "billing") return;
  const projected = billing.value?.subscriptions.some((item) => item.state !== "canceled" && item.state !== "incomplete_expired");
  returnState.value = projected ? "projected" : "pending";
  if (projected) {
    void emit("subscription_projected", { offer_code: selectedOfferCode.value, result: "active" });
    return;
  }
  if (pollCount >= 12) return;
  pollTimer = window.setTimeout(async () => {
    pollCount += 1;
    await loadBilling();
    evaluateReturn();
  }, 2500);
}

async function load(): Promise<void> {
  loading.value = true;
  errorMessage.value = "";
  try {
    const [published, consent] = await Promise.all([getPublicCatalog(), getPrivacyConsent()]);
    catalog.value = published;
    analyticsAllowed.value = consent.decided && consent.analytics && !consent.renewal_required;
    chooseOffer();
    const proposed = typeof route.query.ref === "string" ? normalizeReferral(route.query.ref) : "";
    if (referralPattern.test(proposed)) {
      referralInput.value = proposed;
      referralEntryMethod.value = "link";
    }
    await loadBilling();
    evaluateReturn();
    if (selectedOfferCode.value) {
      void emit("checkout_reviewed", { offer_code: selectedOfferCode.value, referral_present: "false" });
    }
  } catch (error) {
    errorMessage.value = error instanceof APIProblem ? error.message : "Checkout is temporarily unavailable.";
  } finally {
    loading.value = false;
  }
}

async function startCheckout(): Promise<void> {
  const accountID = session.selectedID;
  const offer = selectedOffer.value;
  if (!accountID || !offer || !canCheckout.value || !confirmed.value || submitting.value) return;
  submitting.value = true;
  errorMessage.value = "";
  requestID.value ||= crypto.randomUUID();
  try {
    const hosted = await createCheckoutSession(accountID, {
      offer_code: offer.code,
      ...(appliedReferral.value ? { affiliate_code: appliedReferral.value } : {})
    }, requestID.value);
    const target = new URL(hosted.url);
    if (target.protocol !== "https:") throw new Error("Checkout returned an unsafe destination.");
    if (appliedReferral.value) {
      await emit("referral_code_accepted", { offer_code: offer.code, entry_method: referralEntryMethod.value });
    }
    await emit("checkout_redirected", { offer_code: offer.code, referral_present: String(referralApplied.value) });
    window.location.assign(target.href);
  } catch (error) {
    if (error instanceof APIProblem && (error.problem?.code === "strong_reauthentication_required" || error.problem?.code === "owner_security_enrollment_required")) {
      const returnTo = encodeURIComponent(`${route.fullPath}`);
      window.location.assign(`/app/security?return_to=${returnTo}&status=${error.problem.code}`);
      return;
    }
    errorMessage.value = error instanceof APIProblem ? error.message : error instanceof Error ? error.message : "Checkout could not be started.";
  } finally {
    submitting.value = false;
  }
}

watch(() => session.selectedID, async (current, previous) => {
  if (current && current !== previous) await loadBilling();
});
watch(selectedOfferCode, () => {
  confirmed.value = false;
  requestID.value = "";
});
onMounted(() => void load());
onBeforeUnmount(() => { if (pollTimer !== undefined) window.clearTimeout(pollTimer); });
</script>

<template>
  <section class="page checkout-page">
    <header class="page-heading">
      <p class="eyebrow">Secure checkout</p>
      <h1>Review before Stripe.</h1>
      <p>Confirm the published offer and any Affiliate referral. Payment details are collected by Stripe; access changes only after Spyglass projects the signed payment event.</p>
    </header>

    <div v-if="returnState === 'cancelled'" class="queue-state queue-state--warning" role="status">
      <h2>Checkout was cancelled</h2><p>No purchase was completed. Your Account and current access are unchanged.</p>
    </div>
    <div v-else-if="returnState === 'pending'" class="queue-state" role="status" aria-live="polite">
      <h2>Payment received; access is being confirmed</h2><p>Spyglass is waiting for Stripe's signed event. You can leave this page safely; access is never granted from a redirect alone.</p>
    </div>
    <div v-else-if="returnState === 'projected'" class="queue-state" role="status">
      <h2>Your subscription is active</h2><p>The local entitlement snapshot now includes the projected subscription.</p>
    </div>

    <div v-if="loading" class="queue-state" role="status">Loading the published Catalog and Account billing state…</div>
    <div v-else-if="errorMessage && !catalog" class="queue-state queue-state--error" role="alert"><h2>Checkout is unavailable</h2><p>{{ errorMessage }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></div>
    <div v-else class="checkout-layout">
      <section class="checkout-card" aria-labelledby="offer-heading">
        <p class="eyebrow">1 · Offer</p><h2 id="offer-heading">Choose the published offer</h2>
        <label for="checkout-offer">Plan and billing interval</label>
        <select id="checkout-offer" v-model="selectedOfferCode">
          <option v-for="offer in paidOffers" :key="offer.code" :value="offer.code">
            {{ catalog?.plans.find((plan) => plan.code === offer.plan_code)?.name ?? offer.plan_code }} · {{ formatPrice(offer) }}/{{ offer.billing_interval }}
          </option>
        </select>
        <div v-if="selectedOffer && selectedPlan" class="offer-summary">
          <strong>{{ selectedPlan.name }} · {{ formatPrice(selectedOffer) }} per {{ selectedOffer.billing_interval }}</strong>
          <p>{{ selectedPlan.description }}</p>
          <ul><li v-for="(mode, packageCode) in selectedPlan.packages" :key="packageCode"><span>{{ packageCode }}</span><small>{{ String(mode).replace('_', ' ') }}</small></li></ul>
        </div>
      </section>

      <section class="checkout-card" aria-labelledby="referral-heading">
        <p class="eyebrow">2 · Referral</p><h2 id="referral-heading">Affiliate code <small>optional</small></h2>
        <p class="form-note">A valid code gives the Affiliate recurring credit under their program terms. It does not change your price and works whether or not you allow analytics.</p>
        <label for="affiliate-code">Affiliate code</label>
        <div class="referral-entry"><input id="affiliate-code" v-model="referralInput" :disabled="referralApplied" autocomplete="off" spellcheck="false" placeholder="IO-PARTNER1" @input="referralError = ''" /><IoButton v-if="!referralApplied" kind="secondary" @click="applyReferral">Apply</IoButton><IoButton v-else kind="secondary" @click="removeReferral">Remove</IoButton></div>
        <p v-if="referralError" class="form-error" role="alert">{{ referralError }}</p>
        <p v-else-if="referralApplied" class="referral-confirmed" role="status">Referral <strong>{{ appliedReferral }}</strong> will be validated by Spyglass when checkout begins.</p>
        <p v-else-if="referralEntryMethod === 'link' && referralInput" class="queue-inline-status">A referral was proposed by your link. Select Apply to use it; it is not attached automatically.</p>
      </section>

      <section class="checkout-card checkout-confirm" aria-labelledby="confirm-heading">
        <p class="eyebrow">3 · Confirm</p><h2 id="confirm-heading">Authorize the handoff</h2>
        <div v-if="!canManage" class="queue-inline-status queue-inline-status--error">Only an Account owner or billing administrator can start checkout.</div>
        <div v-else-if="billing && !billing.can_start_checkout" class="queue-inline-status">This Account already has a managed subscription or cannot start another checkout.</div>
        <label class="confirmation"><input v-model="confirmed" type="checkbox" :disabled="!canCheckout" /><span>I confirm this offer<span v-if="referralApplied"> and Affiliate referral</span>, and I want to continue to Stripe.</span></label>
        <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
        <IoButton :disabled="!canCheckout || !confirmed || submitting || !selectedOffer" @click="startCheckout">{{ submitting ? "Opening Stripe…" : "Continue to Stripe" }}</IoButton>
        <p class="form-note">Repeated taps and recoverable retries reuse one checkout request. Spyglass never sends provider price identifiers from this browser.</p>
      </section>
    </div>
  </section>
</template>
