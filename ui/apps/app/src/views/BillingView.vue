<script setup lang="ts">
import { APIProblem, createBillingPortalSession, getAITokenBalance, getBillingStatus, getPublicCatalog, type AITokenBalance, type BillingStatus, type BillingSubscription, type PublicCatalog } from "@spyglass/api";
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
const error = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const requestID = ref("");
let sequence = 0;

const managed = computed(() => billing.value?.subscriptions.filter((item) => item.state !== "canceled" && item.state !== "incomplete_expired") ?? []);
const canOpenPortal = computed(() => billing.value?.can_manage === true && billing.value.has_customer);
const returned = computed(() => route.query.status === "portal_returned");
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
  pending: opening,
  onBlocked: () => { navigationNotice.value = "Stripe billing management is still being prepared. Stay on this page until Spyglass confirms the handoff."; }
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
  const accountID = session.selectedID; if (!accountID || !canOpenPortal.value || opening.value) return; opening.value = true; error.value = ""; navigationNotice.value = ""; requestID.value ||= crypto.randomUUID();
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
watch(() => session.selectedID, (current, previous) => { if (current !== previous) void load(); });
onMounted(() => void load());
</script>

<template>
  <section class="page billing-page">
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading"><p class="eyebrow">Billing &amp; access</p><h1>Know what the Account pays for</h1><p>Spyglass shows its local subscription projection here. Stripe collects payment details and hosts subscription management; package access changes only after a signed provider event is projected.</p></header>
    <section v-if="returned" class="queue-state" role="status"><h2>Welcome back from Stripe</h2><p>Billing changes may take a moment to appear while Spyglass verifies and projects the signed event.</p><IoButton kind="secondary" @click="load">Refresh billing state</IoButton></section>
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
        <div class="billing-actions"><IoButton v-if="canOpenPortal" :disabled="opening" @click="openPortal">{{ opening ? "Opening Stripe…" : "Manage in Stripe" }}</IoButton><a v-if="billing.can_start_checkout" class="io-link-button" href="/app/checkout">Review paid plans</a></div>
      </section>
      <section class="billing-history" aria-labelledby="ai-token-heading">
        <header><div><p class="eyebrow">Shared team usage</p><h2 id="ai-token-heading">Infinite Ocean AI Tokens</h2></div><span v-if="tokens">{{ quantity(tokens.available) }} available</span></header>
        <p>AI Tokens are provider-neutral usage credits, not money or raw vendor tokens. Every run freezes its complexity rate before work begins and settles only trusted usage.</p>
        <p v-if="tokenNotice" class="queue-inline-status" role="status">{{ tokenNotice }}</p>
        <dl v-else-if="tokens" class="billing-token-grid">
          <div><dt>Available</dt><dd>{{ quantity(tokens.available) }}</dd></div><div><dt>Reserved</dt><dd>{{ quantity(tokens.reserved) }}</dd></div><div><dt>Included</dt><dd>{{ quantity(tokens.included) }}</dd></div><div><dt>Purchased</dt><dd>{{ quantity(tokens.purchased) }}</dd></div><div><dt>Promotional</dt><dd>{{ quantity(tokens.promotion) }}</dd></div><div><dt>Used to date</dt><dd>{{ quantity(tokens.consumed) }}</dd></div>
        </dl>
        <div v-if="catalog" class="billing-token-offers">
          <p><strong>{{ quantity(catalog.ai_token_renewal_grant.quantity) }} included per paid renewal.</strong> Unused included Tokens reset when the next successful renewal grant arrives.</p>
          <p v-for="bundle in catalog.ai_token_bundles" :key="`${bundle.code}:${bundle.version}`"><strong>{{ quantity(bundle.quantity) }} Tokens · {{ money(bundle.amount_minor, bundle.currency) }}</strong><br><span>{{ bundle.disclosure }}</span></p>
          <small>Top-ups are manual—there is no automatic replenishment or surprise overage charge. Purchase checkout will appear only when the Account and strong-auth requirements are satisfied.</small>
        </div>
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
