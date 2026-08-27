<script setup lang="ts">
import {
  APIProblem,
  createBillingPortalSession,
  createPurchaseCheckoutSession,
  getAITokenBalance,
  getBillingStatus,
  getPublicCatalog,
  redeemAITokenPromotion,
  type AITokenBalance,
  type BillingStatus,
  type BillingSubscription,
  type PublicCatalog,
  type PurchaseKind
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const route = useRoute();
const billing = ref<BillingStatus>();
const catalog = ref<PublicCatalog>();
const tokens = ref<AITokenBalance>();
const tokenNotice = ref("");
const loading = ref(false);
const opening = ref(false);
const purchasing = ref("");
const redeeming = ref(false);
const error = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const requestID = ref("");
const purchaseNotice = ref("");
const promotionCode = ref("");
const promotionError = ref("");
const promotionNotice = ref("");
const promotionRequestID = ref("");
const purchaseRequestIDs = new Map<string, string>();
let sequence = 0;

const managed = computed(() => billing.value?.subscriptions.filter((item) => item.state !== "canceled" && item.state !== "incomplete_expired") ?? []);
const canOpenPortal = computed(() => billing.value?.can_manage === true && billing.value.has_customer);
const canPurchase = computed(() => billing.value?.can_manage === true
  && !billing.value.lifecycle
  && managed.value.some((item) => item.state === "active" || item.state === "trialing"));
const actionPending = computed(() => opening.value || purchasing.value !== "" || redeeming.value);
const portalReturned = computed(() => route.query.status === "portal_returned");
const purchaseReturned = computed(() => route.query.status === "purchase_returned");
const purchaseCancelled = computed(() => route.query.status === "purchase_cancelled");
const returnedPurchaseKind = computed(() => route.query.purchase === "commissioning" ? "commissioning" : "AI Token top-up");
const lifecycleTitle = computed(() => {
  const lifecycle = billing.value?.lifecycle;
  if (!lifecycle) return "";
  if (lifecycle.state === "cancellation_scheduled") return "Cancellation is scheduled";
  if (lifecycle.state === "grace_read_only") return "Payment needs attention";
  if (lifecycle.state === "termination_pending") return "Deletion deadline reached";
  return "Account access is restricted";
});
const lifecycleMessage = computed(() => {
  const lifecycle = billing.value?.lifecycle;
  if (!lifecycle) return "";
  if (lifecycle.state === "cancellation_scheduled") return `Paid access continues until ${date(lifecycle.effective_at)}. Resuming the subscription before then cancels this lifecycle.`;
  if (lifecycle.state === "grace_read_only") return `The Account is read-only while payment is unresolved. Broader restriction begins ${date(lifecycle.restriction_at)}.`;
  if (lifecycle.state === "termination_pending") return "Provider termination is being verified. The reviewed Account-erasure workflow remains the final safety gate.";
  return `Only billing, security, privacy, and data export remain available. Restore billing before ${date(lifecycle.delete_at)} to prevent the deletion handoff.`;
});
const { allowNextNavigation } = useSafeNavigation({
  dirty: false,
  pending: actionPending,
  onBlocked: () => { navigationNotice.value = "A billing action is still being prepared. Stay on this page until Spyglass confirms the handoff."; }
});

function date(value?: string): string {
  return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Not scheduled";
}
function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function quantity(value?: number): string { return new Intl.NumberFormat().format(value ?? 0); }
function money(value: number, currency: string): string { return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(value / 100); }
function planName(subscription: BillingSubscription): string {
  const offer = catalog.value?.offers.find((item) => item.code === subscription.offer_code);
  const plan = catalog.value?.plans.find((item) => item.code === offer?.plan_code && item.version === offer.plan_version);
  return plan?.name ?? subscription.offer_code;
}
function period(subscription: BillingSubscription): string {
  if (!subscription.current_period_start && !subscription.current_period_end) return "No current billing period";
  return `${date(subscription.current_period_start)} – ${date(subscription.current_period_end)}`;
}
async function load(): Promise<void> {
  const accountID = session.selectedID; const current = ++sequence; billing.value = undefined; tokens.value = undefined; tokenNotice.value = ""; error.value = ""; requestID.value = "";
  if (!accountID) return; loading.value = true;
  try {
    const [status, publication] = await Promise.all([getBillingStatus(accountID), getPublicCatalog()]);
    if (current === sequence) { billing.value = status; catalog.value = publication; }
    try { const balance = await getAITokenBalance(accountID); if (current === sequence) tokens.value = balance; }
    catch (cause) {
      if (current === sequence) tokenNotice.value = cause instanceof APIProblem && cause.problem?.code === "owner_security_enrollment_required"
        ? "Finish owner security setup to inspect this team's AI Token ledger."
        : "AI Token totals are temporarily unavailable.";
    }
  } catch (cause) {
    if (current === sequence) error.value = cause instanceof APIProblem ? cause.message : "Billing details are temporarily unavailable.";
  } finally { if (current === sequence) loading.value = false; }
}
async function openPortal(): Promise<void> {
  const accountID = session.selectedID; if (!accountID || !canOpenPortal.value || actionPending.value) return; opening.value = true; error.value = ""; navigationNotice.value = ""; requestID.value ||= crypto.randomUUID();
  try {
    const hosted = await createBillingPortalSession(accountID, requestID.value); const target = new URL(hosted.url);
    if (target.protocol !== "https:") throw new Error("Billing management returned an unsafe destination.");
    announcement.value = "Opening Stripe billing management."; allowNextNavigation(); window.location.assign(target.href);
  } catch (cause) {
    if (cause instanceof APIProblem && (cause.problem?.code === "strong_reauthentication_required" || cause.problem?.code === "owner_security_enrollment_required")) {
      allowNextNavigation(); window.location.assign(`/app/security?return_to=%2Fapp%2Fbilling&status=${encodeURIComponent(cause.problem.code)}`); return;
    }
    error.value = cause instanceof APIProblem ? cause.message : cause instanceof Error ? cause.message : "Billing management could not be opened.";
  } finally { opening.value = false; }
}
async function startPurchase(kind: PurchaseKind, itemCode: string): Promise<void> {
  const accountID = session.selectedID;
  const key = `${kind}:${itemCode}`;
  if (!accountID || !canPurchase.value || actionPending.value) return;
  purchasing.value = key;
  purchaseNotice.value = "";
  navigationNotice.value = "";
  const stableRequestID = purchaseRequestIDs.get(key) ?? crypto.randomUUID();
  purchaseRequestIDs.set(key, stableRequestID);
  try {
    const hosted = await createPurchaseCheckoutSession(accountID, { kind, item_code: itemCode }, stableRequestID);
    const target = new URL(hosted.url);
    if (target.protocol !== "https:") throw new Error("Purchase checkout returned an unsafe destination.");
    announcement.value = "Opening secure purchase checkout.";
    allowNextNavigation();
    window.location.assign(target.href);
  } catch (cause) {
    if (cause instanceof APIProblem && (cause.problem?.code === "strong_reauthentication_required" || cause.problem?.code === "owner_security_enrollment_required")) {
      allowNextNavigation();
      window.location.assign(`/app/security?return_to=%2Fapp%2Fbilling&status=${encodeURIComponent(cause.problem.code)}`);
      return;
    }
    if (cause instanceof APIProblem && cause.problem?.code === "commissioning_already_purchased" && billing.value) {
      billing.value = { ...billing.value, commissioning_purchased: true };
    }
    purchaseNotice.value = cause instanceof APIProblem ? cause.message : cause instanceof Error ? cause.message : "Purchase checkout could not be opened.";
  } finally {
    purchasing.value = "";
  }
}
function notePromotionEdit(): void {
  promotionError.value = "";
  promotionNotice.value = "";
  promotionRequestID.value = "";
}
async function redeemPromotion(): Promise<void> {
  const accountID = session.selectedID;
  const normalized = promotionCode.value.trim().toLowerCase();
  if (!accountID || !canPurchase.value || actionPending.value) return;
  if (!/^[a-z][a-z0-9_]{0,63}$/.test(normalized)) {
    promotionError.value = "Enter the complete promotion code using letters, numbers, or underscores.";
    return;
  }
  promotionCode.value = normalized;
  promotionError.value = "";
  promotionNotice.value = "";
  redeeming.value = true;
  promotionRequestID.value ||= crypto.randomUUID();
  try {
    const redemption = await redeemAITokenPromotion(accountID, { promotion_code: normalized }, promotionRequestID.value);
    tokens.value = redemption.balance;
    promotionNotice.value = `${quantity(redemption.grant.quantity)} promotional AI Tokens were added and expire ${date(redemption.grant.expires_at)}.`;
    announcement.value = promotionNotice.value;
  } catch (cause) {
    if (cause instanceof APIProblem && (cause.problem?.code === "strong_reauthentication_required" || cause.problem?.code === "owner_security_enrollment_required")) {
      allowNextNavigation();
      window.location.assign(`/app/security?return_to=%2Fapp%2Fbilling&status=${encodeURIComponent(cause.problem.code)}`);
      return;
    }
    promotionError.value = cause instanceof APIProblem ? cause.message : "The promotion could not be redeemed.";
  } finally {
    redeeming.value = false;
  }
}
watch(() => session.selectedID, (current, previous) => {
  if (current === previous) return;
  purchaseRequestIDs.clear();
  promotionCode.value = "";
  promotionError.value = "";
  promotionNotice.value = "";
  promotionRequestID.value = "";
  void load();
});
onMounted(() => void load());
</script>

<template>
  <section class="page billing-page">
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading"><p class="eyebrow">Billing &amp; access</p><h1>Know what the Account pays for</h1><p>Spyglass shows its local subscription projection here. Stripe collects payment details and hosts subscription management; package access changes only after a signed provider event is projected.</p></header>
    <section v-if="portalReturned" class="queue-state" role="status"><h2>Welcome back from Stripe</h2><p>Billing changes may take a moment to appear while Spyglass verifies and projects the signed event.</p><IoButton kind="secondary" @click="load">Refresh billing state</IoButton></section>
    <section v-else-if="purchaseReturned" class="queue-state" role="status"><h2>{{ returnedPurchaseKind }} payment returned</h2><p>Spyglass is waiting for Stripe's signed payment event before recording the purchase. Refresh this page to inspect the newest local projection.</p><IoButton kind="secondary" @click="load">Refresh purchase state</IoButton></section>
    <section v-else-if="purchaseCancelled" class="queue-state queue-state--warning" role="status"><h2>{{ returnedPurchaseKind }} checkout was cancelled</h2><p>No one-time purchase was recorded. Existing subscription access and AI Token balances are unchanged.</p></section>
    <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Billing state always belongs to one Account.</p></section>
    <div v-else-if="loading" class="queue-state" role="status">Loading Account billing state…</div>
    <section v-else-if="error && !billing" class="queue-state queue-state--error" role="alert"><h2>Billing is unavailable</h2><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></section>
    <template v-else-if="billing">
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section v-if="billing.lifecycle" class="queue-state" :class="{ 'queue-state--error': billing.lifecycle.state === 'termination_pending' }" role="status">
        <p class="eyebrow">Subscription lifecycle</p><h2>{{ lifecycleTitle }}</h2><p>{{ lifecycleMessage }}</p>
        <dl class="billing-token-grid"><div><dt>Trigger</dt><dd>{{ label(billing.lifecycle.trigger) }}</dd></div><div><dt>Effective</dt><dd>{{ date(billing.lifecycle.effective_at) }}</dd></div><div><dt>Restricted</dt><dd>{{ date(billing.lifecycle.restriction_at) }}</dd></div><div><dt>Deletion handoff</dt><dd>{{ date(billing.lifecycle.delete_at) }}</dd></div></dl>
      </section>
      <section class="billing-summary-card">
        <div><p class="eyebrow">Current access</p><h2>{{ managed.length ? `${managed.length} managed subscription${managed.length === 1 ? '' : 's'}` : 'Checkout required' }}</h2><p>{{ billing.has_customer ? "This Account has a Stripe customer record." : "No subscription is active. Infinite Ocean has no free plan." }}</p></div>
        <div class="billing-actions"><IoButton v-if="canOpenPortal" :disabled="actionPending" @click="openPortal">{{ opening ? "Opening Stripe…" : "Manage in Stripe" }}</IoButton><a v-if="billing.can_start_checkout" class="io-link-button" href="/app/checkout">Review paid plans</a></div>
      </section>
      <p v-if="purchaseNotice" class="queue-inline-status queue-inline-status--error" role="alert">{{ purchaseNotice }}</p>
      <section class="billing-history" aria-labelledby="ai-token-heading">
        <header><div><p class="eyebrow">Shared team usage</p><h2 id="ai-token-heading">Infinite Ocean AI Tokens</h2></div><span v-if="tokens">{{ quantity(tokens.available) }} available</span></header>
        <p>AI Tokens are provider-neutral usage credits, not money or raw vendor tokens. Every run freezes its complexity rate before work begins and settles only trusted usage.</p>
        <p v-if="tokenNotice" class="queue-inline-status" role="status">{{ tokenNotice }}</p>
        <dl v-else-if="tokens" class="billing-token-grid">
          <div><dt>Available</dt><dd>{{ quantity(tokens.available) }}</dd></div><div><dt>Reserved</dt><dd>{{ quantity(tokens.reserved) }}</dd></div><div><dt>Included</dt><dd>{{ quantity(tokens.included) }}</dd></div><div><dt>Purchased</dt><dd>{{ quantity(tokens.purchased) }}</dd></div><div><dt>Promotional</dt><dd>{{ quantity(tokens.promotion) }}</dd></div><div><dt>Used to date</dt><dd>{{ quantity(tokens.consumed) }}</dd></div>
        </dl>
        <div v-if="catalog" class="billing-token-offers">
          <p><strong>{{ quantity(catalog.ai_token_renewal_grant.quantity) }} included per paid renewal.</strong> Unused included Tokens reset when the next successful renewal grant arrives.</p>
          <article v-for="bundle in catalog.ai_token_bundles" :key="`${bundle.code}:${bundle.version}`" class="billing-purchase-card">
            <div><strong>{{ quantity(bundle.quantity) }} Tokens · {{ money(bundle.amount_minor, bundle.currency) }}</strong><span>{{ bundle.disclosure }}</span></div>
            <IoButton kind="secondary" :disabled="!canPurchase || actionPending" :aria-label="`Buy ${quantity(bundle.quantity)} AI Tokens for ${money(bundle.amount_minor, bundle.currency)}`" @click="startPurchase('ai_token_top_up', bundle.code)">{{ purchasing === `ai_token_top_up:${bundle.code}` ? "Opening Stripe…" : "Buy Tokens" }}</IoButton>
          </article>
          <small>Top-ups are manual—there is no automatic replenishment or surprise overage charge. Prices and quantities are frozen from the current Catalog before Stripe opens.</small>
          <p v-if="!canPurchase" class="queue-inline-status">An active subscription plus owner or billing-administrator authority is required for one-time purchases.</p>
        </div>
        <form v-if="catalog" class="billing-promotion" @submit.prevent="redeemPromotion">
          <div><p class="eyebrow">Promotion</p><h3>Redeem a promotion code</h3><p>Promotional AI Tokens follow the campaign's quantity, expiry, eligibility and redemption limits. Promotions do not stack automatically.</p></div>
          <label for="promotion-code">Promotion code</label>
          <div class="billing-promotion-entry"><input id="promotion-code" v-model="promotionCode" autocomplete="off" spellcheck="false" :disabled="!canPurchase || redeeming" @input="notePromotionEdit"><IoButton type="submit" kind="secondary" :disabled="!canPurchase || actionPending || !promotionCode.trim()">{{ redeeming ? "Redeeming…" : "Redeem" }}</IoButton></div>
          <p v-if="promotionError" class="form-error" role="alert">{{ promotionError }}</p><p v-if="promotionNotice" class="referral-confirmed" role="status">{{ promotionNotice }}</p>
        </form>
      </section>
      <section v-if="catalog?.commissioning_offer" class="billing-history" aria-labelledby="commissioning-heading">
        <header><div><p class="eyebrow">Optional setup</p><h2 id="commissioning-heading">Commissioning package</h2></div><span>One time</span></header>
        <div class="billing-commissioning">
          <div><strong>{{ money(catalog.commissioning_offer.amount_minor, catalog.commissioning_offer.currency) }}</strong><p>{{ catalog.commissioning_offer.disclosure }}</p><small>Commissioning is optional, adds no software entitlement, and earns no Affiliate commission.</small></div>
          <span v-if="billing.commissioning_purchased" class="state-badge">Purchased</span>
          <IoButton v-else kind="secondary" :disabled="!canPurchase || actionPending" @click="startPurchase('commissioning', catalog.commissioning_offer.code)">{{ purchasing === `commissioning:${catalog.commissioning_offer.code}` ? "Opening Stripe…" : "Purchase commissioning" }}</IoButton>
        </div>
        <p v-if="billing.commissioning_purchased" class="queue-inline-status">Standard commissioning is already recorded for this Account. Contact Support for a case-by-case follow-up engagement.</p>
      </section>
      <section class="billing-history">
        <header><div><p class="eyebrow">Local projection</p><h2>Subscription history</h2></div><span>{{ billing.subscriptions.length }} records</span></header>
        <div v-if="billing.subscriptions.length === 0" class="queue-state"><h3>No subscription history</h3><p>This inactive team shell has no product entitlements until Stripe confirms a successful checkout.</p></div>
        <ol v-else class="billing-list"><li v-for="item in billing.subscriptions" :key="`${item.offer_code}-${item.catalog_version}-${item.last_synced_at}`"><div><span class="state-badge" :class="{ 'state-badge--warning': ['past_due', 'unpaid', 'incomplete'].includes(item.state) }">{{ label(item.state) }}</span><h3>{{ planName(item) }}</h3><small>{{ item.offer_code }} · Catalog {{ item.catalog_version }}</small><dl><div><dt>Billing period</dt><dd>{{ period(item) }}</dd></div><div><dt>Cancel at</dt><dd>{{ date(item.cancel_at) }}</dd></div><div><dt>Last verified</dt><dd>{{ date(item.last_synced_at) }}</dd></div></dl></div></li></ol>
      </section>
      <section v-if="!billing.can_manage" class="queue-state"><h2>Billing administrator access required</h2><p>You can inspect this Account's subscription state, but only its owner or billing administrator can start checkout or open Stripe billing management.</p></section>
    </template>
  </section>
</template>
