<script setup lang="ts">
import {
  APIProblem, beginOperationsPasskeyLogin, completeOperationsPasskeyLogin,
  createOperationsSupportGrant, logoutOperations, openOperationsSupportView,
  operationsAnalyticsReport, operationsLookup, operationsSession,
  operationsBillingFailures, operationsReplayBillingEvent, operationsRefreshBillingSubscription,
  operationsOpenPrivacyRights, operationsInspectPrivacyRight, operationsStartPrivacyReview, operationsResolvePrivacyRight,
  operationsInspectAffiliate, operationsInspectAffiliateRisk, operationsTransitionAffiliate,
  revokeOperationsSupportGrant,
  type OperationsAffiliateEnrollment, type OperationsAffiliateRiskEnvelope,
  type OperationsAnalyticsReport, type OperationsLookupKind,
  type OperationsLookupResult, type OperationsSession,
  type OperationsBillingFailuresReport, type OperationsPrivacyQueueItem, type OperationsPrivacyRequest,
  type OperationsSupportViewEnvelope
} from "@spyglass/api";
import { IoButton, IoLogo } from "@spyglass/design-system";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { getOperationsAssertion } from "./webauthn";

type Screen = "overview" | "lookup" | "analytics" | "billing" | "privacy" | "affiliate";
const screen = ref<Screen>("overview");
const session = ref<OperationsSession>();
const loadingSession = ref(true);
const busy = ref(false);
const error = ref("");
const menuOpen = ref(false);
const isDesktop = ref(typeof window !== "undefined" && window.innerWidth >= 1024);
const lookupKind = ref<OperationsLookupKind>("email");
const lookupValue = ref("");
const ticket = ref("");
const reason = ref("");
const lookupResults = ref<ReadonlyArray<OperationsLookupResult>>([]);
const supportView = ref<OperationsSupportViewEnvelope>();
const analytics = ref<OperationsAnalyticsReport>();
const analyticsDays = ref(7);
const analyticsDimension = ref("route_name");
const analyticsMinimum = ref(20);
const billingMode = ref<"test" | "live">("test");
const billingTarget = ref("");
const billing = ref<OperationsBillingFailuresReport>();
const privacyItems = ref<ReadonlyArray<OperationsPrivacyQueueItem>>([]);
const privacyRequestID = ref("");
const privacyRequest = ref<OperationsPrivacyRequest>();
const privacyResolutionState = ref<"completed" | "partially_completed" | "declined">("completed");
const privacyEvidenceID = ref("");
const privacyEvidenceSHA256 = ref("");
const affiliateID = ref("");
const affiliate = ref<OperationsAffiliateEnrollment>();
const affiliateRisk = ref<OperationsAffiliateRiskEnvelope>();

const roles = computed(() => session.value?.staff.roles ?? []);
const canSupport = computed(() => roles.value.some((role) => role === "support" || role === "operations_administrator"));
const canAnalytics = computed(() => roles.value.some((role) => role === "analytics" || role === "operations_administrator"));
const canBilling = computed(() => roles.value.some((role) => role === "billing" || role === "operations_administrator"));
const canPrivacy = computed(() => roles.value.some((role) => role === "privacy" || role === "operations_administrator"));
const canAffiliate = computed(() => roles.value.some((role) => role === "affiliate" || role === "operations_administrator"));
const grantActive = computed(() => supportView.value?.view.grant.state === "active");
const navigationUnavailable = computed(() => !isDesktop.value && !menuOpen.value);

function message(value: unknown): string {
  if (value instanceof APIProblem) return value.message;
  if (value instanceof Error) return value.message;
  return "The request could not be completed.";
}

async function loadSession(): Promise<void> {
  loadingSession.value = true;
  try { session.value = await operationsSession(); }
  catch (value) { if (!(value instanceof APIProblem && value.status === 401)) error.value = message(value); }
  finally { loadingSession.value = false; }
}

async function signIn(): Promise<void> {
  busy.value = true; error.value = "";
  try {
    const ceremony = await beginOperationsPasskeyLogin();
    const credential = await getOperationsAssertion(ceremony);
    session.value = await completeOperationsPasskeyLogin(ceremony.ceremony_id, credential, "operations-console");
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function signOut(): Promise<void> {
  busy.value = true; error.value = "";
  try {
    if (grantActive.value) await revokeGrant("Staff signed out of the support console.");
    await logoutOperations();
    session.value = undefined; supportView.value = undefined; lookupResults.value = [];
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

function navigate(destination: Screen): void {
  screen.value = destination; menuOpen.value = false; error.value = "";
}

async function findAccount(): Promise<void> {
  busy.value = true; error.value = ""; lookupResults.value = [];
  try {
    const response = await operationsLookup({ kind: lookupKind.value, value: lookupValue.value.trim(), ticket: ticket.value.trim(), reason: reason.value.trim() });
    lookupResults.value = response.results;
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function inspectAccount(result: OperationsLookupResult): Promise<void> {
  busy.value = true; error.value = "";
  try {
    if (grantActive.value) await revokeGrant("Staff moved to another customer support view.");
    const granted = await createOperationsSupportGrant({
      account_id: result.account_id, target_user_id: result.user_id,
      lifetime_seconds: 900, ticket: ticket.value.trim(), reason: reason.value.trim()
    });
    supportView.value = await openOperationsSupportView(granted.grant.id, { ticket: ticket.value.trim(), reason: reason.value.trim() });
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function revokeGrant(revokeReason = "Support review completed."): Promise<void> {
  const grant = supportView.value?.view.grant;
  if (!grant || grant.state !== "active") return;
  const revoked = await revokeOperationsSupportGrant(grant.id, {
    expected_version: grant.version, ticket: grant.ticket, reason: revokeReason
  });
  if (supportView.value) supportView.value = {
    ...supportView.value, view: { ...supportView.value.view, grant: revoked.grant }
  };
}

async function closeSupportView(): Promise<void> {
  busy.value = true; error.value = "";
  try { await revokeGrant(); supportView.value = undefined; }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function loadAnalytics(): Promise<void> {
  busy.value = true; error.value = "";
  try {
    const to = new Date();
    const from = new Date(to.getTime() - analyticsDays.value * 86_400_000);
    analytics.value = await operationsAnalyticsReport({
      from: from.toISOString(), to: to.toISOString(), bucket: "day",
      dimension: analyticsDimension.value, minimum_cohort: analyticsMinimum.value,
      ticket: ticket.value.trim(), reason: reason.value.trim()
    });
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

function auditInput() { return { ticket: ticket.value.trim(), reason: reason.value.trim() }; }

async function loadBillingFailures(): Promise<void> {
  busy.value = true; error.value = "";
  try { billing.value = await operationsBillingFailures({ limit: 50, mode: billingMode.value, ...auditInput() }); }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function runBillingAction(kind: "event" | "subscription", target = billingTarget.value): Promise<void> {
  busy.value = true; error.value = "";
  try {
    const response = kind === "event"
      ? await operationsReplayBillingEvent(target.trim(), { mode: billingMode.value, ...auditInput() })
      : await operationsRefreshBillingSubscription(target.trim(), { mode: billingMode.value, ...auditInput() });
    billing.value = { batch_id: response.batch_id, records: [response.record] };
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function loadPrivacyQueue(): Promise<void> {
  busy.value = true; error.value = "";
  try {
    const due = new Date(); due.setUTCDate(due.getUTCDate() + 31);
    privacyItems.value = (await operationsOpenPrivacyRights({ due_before: due.toISOString(), limit: 100, ...auditInput() })).items;
  } catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function inspectPrivacy(requestID = privacyRequestID.value): Promise<void> {
  busy.value = true; error.value = ""; privacyRequestID.value = requestID;
  try { privacyRequest.value = (await operationsInspectPrivacyRight(requestID.trim(), auditInput())).request; }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function startPrivacyReview(): Promise<void> {
  if (!privacyRequest.value) return;
  busy.value = true; error.value = "";
  try { privacyRequest.value = (await operationsStartPrivacyReview(privacyRequest.value.request_id, { expected_version: privacyRequest.value.version, ...auditInput() })).request; }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function resolvePrivacy(): Promise<void> {
  if (!privacyRequest.value) return;
  busy.value = true; error.value = "";
  try { privacyRequest.value = (await operationsResolvePrivacyRight(privacyRequest.value.request_id, {
    expected_version: privacyRequest.value.version, state: privacyResolutionState.value,
    evidence_id: privacyEvidenceID.value.trim(), evidence_sha256: privacyEvidenceSHA256.value.trim(), ...auditInput()
  })).request; }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function inspectAffiliate(): Promise<void> {
  busy.value = true; error.value = ""; affiliateRisk.value = undefined;
  try { affiliate.value = (await operationsInspectAffiliate(affiliateID.value.trim(), auditInput())).enrollment; }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function inspectAffiliateRisk(): Promise<void> {
  busy.value = true; error.value = "";
  try { affiliateRisk.value = await operationsInspectAffiliateRisk(affiliateID.value.trim(), auditInput()); }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

async function transitionAffiliate(state: "active" | "suspended" | "closed"): Promise<void> {
  if (!affiliate.value) return;
  busy.value = true; error.value = "";
  try { affiliate.value = (await operationsTransitionAffiliate(affiliate.value.affiliate_id, { expected_version: affiliate.value.version, state, ...auditInput() })).enrollment; }
  catch (value) { error.value = message(value); }
  finally { busy.value = false; }
}

function formatDate(value?: string | null): string {
  return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Not set";
}

function trackViewport(): void { isDesktop.value = window.innerWidth >= 1024; }
onMounted(() => { window.addEventListener("resize", trackViewport); void loadSession(); });
onBeforeUnmount(() => window.removeEventListener("resize", trackViewport));
</script>

<template>
  <div v-if="loadingSession" class="centered" aria-live="polite">Checking staff session…</div>
  <main v-else-if="!session" class="login-shell">
    <section class="login-card" aria-labelledby="login-title">
      <IoLogo />
      <p class="eyebrow">Staff only</p>
      <h1 id="login-title">Operations Console</h1>
      <p>Use your staff passkey. Customer passwords and customer sessions cannot open this console.</p>
      <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
      <IoButton :disabled="busy" @click="signIn">{{ busy ? "Waiting for passkey…" : "Sign in with passkey" }}</IoButton>
    </section>
  </main>
  <div v-else class="shell">
    <header class="topbar">
      <button class="menu-button" type="button" :aria-expanded="menuOpen" aria-controls="operations-navigation" @click="menuOpen = !menuOpen">Menu</button>
      <IoLogo />
      <span class="staff-name">{{ session.staff.display_name }}</span>
    </header>
    <aside id="operations-navigation" class="sidebar" :class="{ 'sidebar--open': menuOpen }" :inert="navigationUnavailable" :aria-hidden="navigationUnavailable || undefined">
      <IoLogo />
      <nav aria-label="Operations navigation">
        <button type="button" :aria-current="screen === 'overview' ? 'page' : undefined" @click="navigate('overview')">Overview</button>
        <button v-if="canSupport" type="button" :aria-current="screen === 'lookup' ? 'page' : undefined" @click="navigate('lookup')">Customer lookup</button>
        <button v-if="canAnalytics" type="button" :aria-current="screen === 'analytics' ? 'page' : undefined" @click="navigate('analytics')">Analytics</button>
        <button v-if="canBilling" type="button" :aria-current="screen === 'billing' ? 'page' : undefined" @click="navigate('billing')">Billing issues</button>
        <button v-if="canPrivacy" type="button" :aria-current="screen === 'privacy' ? 'page' : undefined" @click="navigate('privacy')">Privacy rights</button>
        <button v-if="canAffiliate" type="button" :aria-current="screen === 'affiliate' ? 'page' : undefined" @click="navigate('affiliate')">Affiliates</button>
      </nav>
      <div class="sidebar-footer">
        <p>{{ session.staff.display_name }}</p>
        <small>{{ roles.join(" · ") }}</small>
        <button type="button" :disabled="busy" @click="signOut">Sign out</button>
      </div>
    </aside>
    <button v-if="menuOpen" class="scrim" type="button" aria-label="Close navigation" @click="menuOpen = false" />
    <div v-if="supportView" class="support-banner" role="status">
      <strong>Read-only support view</strong>
      <span>{{ supportView.view.account.display_name }} · {{ supportView.view.grant.ticket }} · expires {{ formatDate(supportView.view.grant.expires_at) }}</span>
      <button type="button" :disabled="busy" @click="closeSupportView">Close and revoke</button>
    </div>
    <main class="content" :class="{ 'content--with-banner': supportView }">
      <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
      <section v-if="screen === 'overview'" aria-labelledby="overview-title">
        <p class="eyebrow">Operations</p>
        <h1 id="overview-title">What needs attention?</h1>
        <p class="lede">Look up a specific customer for a support case, or review privacy-bounded product signals. Every access carries your name, ticket, and reason.</p>
        <div class="action-grid">
          <button v-if="canSupport" type="button" class="action-card" @click="navigate('lookup')"><strong>Help a customer</strong><span>Exact lookup, a 15-minute grant, and a read-only account view.</span></button>
          <button v-if="canAnalytics" type="button" class="action-card" @click="navigate('analytics')"><strong>Review analytics</strong><span>Aggregated trends with minimum cohort protection.</span></button>
          <button v-if="canBilling" type="button" class="action-card" @click="navigate('billing')"><strong>Fix billing queues</strong><span>Inspect classified failures and explicitly replay one event or refresh one subscription.</span></button>
          <button v-if="canPrivacy" type="button" class="action-card" @click="navigate('privacy')"><strong>Fulfill privacy requests</strong><span>Review the minimized deadline queue and record evidence-bound outcomes.</span></button>
          <button v-if="canAffiliate" type="button" class="action-card" @click="navigate('affiliate')"><strong>Review an Affiliate</strong><span>Inspect one exact enrollment and make a version-fenced lifecycle decision.</span></button>
        </div>
        <section class="guardrail-card" aria-labelledby="guardrails-title">
          <h2 id="guardrails-title">Built-in guardrails</h2>
          <ul><li>No broad customer directory or fuzzy search.</li><li>No customer mutations while viewing an account.</li><li>Every lookup, grant, view, and revocation is recorded.</li></ul>
        </section>
      </section>

      <section v-else-if="screen === 'lookup'" aria-labelledby="lookup-title">
        <p class="eyebrow">Support</p><h1 id="lookup-title">Find one customer</h1>
        <p class="lede">Use an exact identifier from the support request. A ticket and plain-language reason are required.</p>
        <form class="form-card" @submit.prevent="findAccount">
          <label>Look up by<select v-model="lookupKind"><option value="email">Email</option><option value="user_id">User ID</option><option value="account_id">Account ID</option><option value="stripe_customer_id">Stripe customer ID</option><option value="stripe_subscription_id">Stripe subscription ID</option></select></label>
          <label>Exact value<input v-model="lookupValue" required autocomplete="off" /></label>
          <div class="form-row"><label>Support ticket<input v-model="ticket" required placeholder="SUP-1042" autocomplete="off" /></label><label>Reason<input v-model="reason" required placeholder="Customer asked us to check billing access" autocomplete="off" /></label></div>
          <IoButton type="submit" :disabled="busy">{{ busy ? "Searching…" : "Find exact match" }}</IoButton>
        </form>
        <p v-if="lookupResults.length === 0 && lookupValue && !busy" class="empty-state">No result loaded. Confirm the exact identifier and search again.</p>
        <div v-else class="result-list">
          <article v-for="result in lookupResults" :key="`${result.user_id}:${result.account_id}`" class="result-card">
            <div><h2>{{ result.account_name }}</h2><p>{{ result.display_name }} · {{ result.email }}</p><small>{{ result.membership_role }} · Account {{ result.account_state }} · User {{ result.user_state }}</small></div>
            <IoButton :disabled="busy" @click="inspectAccount(result)">Open 15-minute view</IoButton>
          </article>
        </div>
        <section v-if="supportView" class="account-view" aria-labelledby="account-view-title">
          <div class="section-heading"><div><p class="eyebrow">Read only</p><h2 id="account-view-title">{{ supportView.view.account.display_name }}</h2></div><span class="state-pill">{{ supportView.view.account.state }}</span></div>
          <div class="detail-grid">
            <article><h3>Customer</h3><dl><dt>Name</dt><dd>{{ supportView.view.user.display_name }}</dd><dt>Email</dt><dd>{{ supportView.view.user.email }}</dd><dt>Email verified</dt><dd>{{ formatDate(supportView.view.user.email_verified_at) }}</dd><dt>Passkeys</dt><dd>{{ supportView.view.user.passkey_count }}</dd><dt>Membership</dt><dd>{{ supportView.view.membership.role }} · {{ supportView.view.membership.state }}</dd></dl></article>
            <article><h3>Billing</h3><dl><dt>Status</dt><dd>{{ supportView.view.billing.state || "Not connected" }}</dd><dt>Offer</dt><dd>{{ supportView.view.billing.offer_code || "Not set" }}</dd><dt>Period ends</dt><dd>{{ formatDate(supportView.view.billing.current_period_end) }}</dd><dt>Cancel at</dt><dd>{{ formatDate(supportView.view.billing.cancel_at) }}</dd><dt>Stripe customer</dt><dd>{{ supportView.view.billing.customer_id || "Not set" }}</dd></dl></article>
            <article><h3>Access</h3><dl><dt>Entitlement version</dt><dd>{{ supportView.view.entitlements.version }}</dd><dt>Catalog version</dt><dd>{{ supportView.view.entitlements.catalog_version }}</dd><dt>AI tokens available</dt><dd>{{ supportView.view.ai_tokens.available.toLocaleString() }}</dd><dt>Consumed</dt><dd>{{ supportView.view.ai_tokens.consumed.toLocaleString() }}</dd><dt>Reserved</dt><dd>{{ supportView.view.ai_tokens.reserved.toLocaleString() }}</dd></dl></article>
            <article><h3>Lifecycle</h3><dl><dt>Status</dt><dd>{{ supportView.view.lifecycle.state || "No active request" }}</dd><dt>Execute after</dt><dd>{{ formatDate(supportView.view.lifecycle.execute_after) }}</dd><dt>Delete after</dt><dd>{{ formatDate(supportView.view.lifecycle.delete_after) }}</dd><dt>Blocker</dt><dd>{{ supportView.view.lifecycle.blocker_code || "None" }}</dd></dl></article>
          </div>
          <article class="history-card"><h3>Support history visible to the customer</h3><p v-if="supportView.view.support_history.length === 0">No prior support access.</p><ol v-else><li v-for="event in supportView.view.support_history" :key="event.id"><strong>{{ event.action }}</strong> by {{ event.staff_display_name }} · {{ event.ticket }}<br /><span>{{ event.reason }} · {{ formatDate(event.occurred_at) }}</span></li></ol></article>
        </section>
      </section>

      <section v-else-if="screen === 'analytics'" aria-labelledby="analytics-title">
        <p class="eyebrow">Privacy-bounded</p><h1 id="analytics-title">Product analytics</h1>
        <p class="lede">Review aggregate behavior without opening customer records. Small cohorts are withheld.</p>
        <form class="form-card" @submit.prevent="loadAnalytics">
          <div class="form-row"><label>Window<select v-model.number="analyticsDays"><option :value="7">Last 7 days</option><option :value="30">Last 30 days</option><option :value="90">Last 90 days</option></select></label><label>Group by<select v-model="analyticsDimension"><option value="none">No extra grouping</option><option value="route_name">Page or route</option><option value="device_class">Device class</option><option value="cta_code">Call to action</option><option value="offer_code">Offer</option><option value="campaign_code">Campaign</option><option value="entry_point">Entry point</option><option value="result">Result</option></select></label><label>Minimum cohort<input v-model.number="analyticsMinimum" type="number" min="5" max="100" required /></label></div>
          <div class="form-row"><label>Review ticket<input v-model="ticket" required autocomplete="off" /></label><label>Reason<input v-model="reason" required autocomplete="off" /></label></div>
          <IoButton type="submit" :disabled="busy">{{ busy ? "Loading…" : "Run report" }}</IoButton>
        </form>
        <div v-if="analytics" class="table-wrap" tabindex="0" aria-label="Scrollable analytics results"><table><caption>{{ analytics.rows.length }} aggregate rows · cohorts smaller than {{ analytics.minimum_cohort }} withheld</caption><thead><tr><th scope="col">Day</th><th scope="col">Event</th><th scope="col">Surface</th><th scope="col">Dimension</th><th scope="col">Events</th><th scope="col">People</th></tr></thead><tbody><tr v-for="row in analytics.rows" :key="`${row.bucket_start}:${row.event_name}:${row.dimension_value}`"><td>{{ formatDate(row.bucket_start) }}</td><td>{{ row.event_name }}</td><td>{{ row.surface }}</td><td>{{ row.dimension_value }}</td><td>{{ row.event_count }}</td><td>{{ row.unique_subjects }}</td></tr></tbody></table></div>
      </section>

      <section v-else-if="screen === 'billing'" aria-labelledby="billing-title">
        <p class="eyebrow">Billing operations</p><h1 id="billing-title">Fix one billing problem</h1>
        <p class="lede">Inspect classified queue failures, then explicitly replay one verified event or refresh one subscription. No provider payload or payment method data is shown.</p>
        <form class="form-card" @submit.prevent="loadBillingFailures">
          <div class="form-row"><label>Stripe mode<select v-model="billingMode"><option value="test">Test</option><option value="live">Live</option></select></label><label>Ticket<input v-model="ticket" required autocomplete="off" /></label><label>Reason<input v-model="reason" required autocomplete="off" /></label></div>
          <IoButton type="submit" :disabled="busy">Inspect failures</IoButton>
        </form>
        <form class="form-card" @submit.prevent>
          <label>Exact event or subscription ID<input v-model="billingTarget" required autocomplete="off" placeholder="evt_… or sub_…" /></label>
          <div class="button-row"><IoButton :disabled="busy || !billingTarget.startsWith('evt_')" @click="runBillingAction('event')">Replay event</IoButton><IoButton kind="secondary" :disabled="busy || !billingTarget.startsWith('sub_')" @click="runBillingAction('subscription')">Refresh subscription</IoButton></div>
        </form>
        <div v-if="billing" class="result-list"><article v-for="record in billing.records" :key="`${record.kind}:${record.target_id}`" class="result-card"><div><h2>{{ record.kind }} · {{ record.state }}</h2><p>{{ record.explanation }}</p><small>{{ record.target_id }} · Account {{ record.account_id }} · attempts {{ record.attempt_count }}</small></div><div class="button-row"><button v-if="record.target_id.startsWith('evt_')" type="button" @click="runBillingAction('event', record.target_id)">Replay</button><button v-if="record.target_id.startsWith('sub_')" type="button" @click="runBillingAction('subscription', record.target_id)">Refresh</button></div></article></div>
        <p v-if="billing && billing.records.length === 0" class="empty-state">No classified billing failures are waiting.</p>
      </section>

      <section v-else-if="screen === 'privacy'" aria-labelledby="privacy-title">
        <p class="eyebrow">Privacy operations</p><h1 id="privacy-title">Fulfill a rights request</h1>
        <p class="lede">The queue is content-minimized. Opening a request and every version-fenced decision are separately audited.</p>
        <form class="form-card" @submit.prevent="loadPrivacyQueue"><div class="form-row"><label>Ticket<input v-model="ticket" required autocomplete="off" /></label><label>Reason<input v-model="reason" required autocomplete="off" /></label></div><IoButton type="submit" :disabled="busy">Load requests due within 31 days</IoButton></form>
        <form class="form-card" @submit.prevent="inspectPrivacy()"><label>Exact request ID<input v-model="privacyRequestID" required autocomplete="off" /></label><IoButton type="submit" :disabled="busy">Inspect request</IoButton></form>
        <div v-if="privacyItems.length" class="result-list"><article v-for="item in privacyItems" :key="item.request_id" class="result-card"><div><h2>{{ item.kind }} · {{ item.state }}</h2><p>{{ item.scope }} · due {{ formatDate(item.response_due_at) }}</p><small>{{ item.request_id }} · version {{ item.version }}</small></div><IoButton :disabled="busy" @click="inspectPrivacy(item.request_id)">Inspect</IoButton></article></div>
        <p v-if="privacyItems.length === 0 && ticket" class="empty-state">No open privacy requests are loaded.</p>
        <article v-if="privacyRequest" class="account-view">
          <div class="section-heading"><div><p class="eyebrow">Exact request</p><h2>{{ privacyRequest.kind }} · {{ privacyRequest.scope }}</h2></div><span class="state-pill">{{ privacyRequest.state }}</span></div><dl><dt>Request</dt><dd>{{ privacyRequest.request_id }}</dd><dt>Version</dt><dd>{{ privacyRequest.version }}</dd><dt>Due</dt><dd>{{ formatDate(privacyRequest.response_due_at) }}</dd></dl><IoButton v-if="privacyRequest.state === 'submitted'" :disabled="busy" @click="startPrivacyReview">Start review</IoButton>
          <form v-if="privacyRequest.state === 'in_review'" class="form-card" @submit.prevent="resolvePrivacy"><div class="form-row"><label>Outcome<select v-model="privacyResolutionState"><option value="completed">Completed</option><option value="partially_completed">Partially completed</option><option value="declined">Declined</option></select></label><label>Evidence ID<input v-model="privacyEvidenceID" required autocomplete="off" /></label></div><label>Evidence SHA-256<input v-model="privacyEvidenceSHA256" required minlength="64" maxlength="64" autocomplete="off" /></label><IoButton type="submit" :disabled="busy">Record outcome</IoButton></form>
        </article>
      </section>

      <section v-else aria-labelledby="affiliate-title">
        <p class="eyebrow">Affiliate operations</p><h1 id="affiliate-title">Review one Affiliate</h1>
        <p class="lede">Use the exact Affiliate ID from the support case. Risk signals are content-free and never make the decision for you.</p>
        <form class="form-card" @submit.prevent="inspectAffiliate"><label>Exact Affiliate ID<input v-model="affiliateID" required autocomplete="off" /></label><div class="form-row"><label>Ticket<input v-model="ticket" required autocomplete="off" /></label><label>Reason<input v-model="reason" required autocomplete="off" /></label></div><div class="button-row"><IoButton type="submit" :disabled="busy">Inspect enrollment</IoButton><IoButton kind="secondary" :disabled="busy || !affiliateID" @click="inspectAffiliateRisk">Inspect risk signals</IoButton></div></form>
        <article v-if="affiliate" class="account-view"><div class="section-heading"><div><p class="eyebrow">Exact enrollment</p><h2>{{ affiliate.public_code }}</h2></div><span class="state-pill">{{ affiliate.state }}</span></div><dl><dt>Affiliate</dt><dd>{{ affiliate.affiliate_id }}</dd><dt>User</dt><dd>{{ affiliate.user_id }}</dd><dt>Settlement Account</dt><dd>{{ affiliate.settlement_account_id || 'Not set' }}</dd><dt>Version</dt><dd>{{ affiliate.version }}</dd></dl><div class="button-row"><IoButton v-if="affiliate.state === 'suspended'" :disabled="busy" @click="transitionAffiliate('active')">Reactivate</IoButton><IoButton v-if="affiliate.state === 'active'" kind="secondary" :disabled="busy" @click="transitionAffiliate('suspended')">Suspend</IoButton><button v-if="affiliate.state !== 'closed'" type="button" class="danger-button" :disabled="busy" @click="transitionAffiliate('closed')">Close permanently</button></div></article>
        <article v-if="affiliateRisk" class="history-card"><h2>Risk signals</h2><p v-if="affiliateRisk.flags.length === 0">No configured signal crossed its review threshold.</p><ul v-else><li v-for="flag in affiliateRisk.flags" :key="flag">{{ flag.replaceAll('_', ' ') }}</li></ul><dl><dt>Valid reservations</dt><dd>{{ affiliateRisk.risk.valid_reservations }}</dd><dt>Distinct Accounts</dt><dd>{{ affiliateRisk.risk.distinct_referred_accounts }}</dd><dt>Locked attributions</dt><dd>{{ affiliateRisk.risk.locked_attributions }}</dd><dt>Largest Account share</dt><dd>{{ (affiliateRisk.risk.largest_account_share_basis_points / 100).toFixed(2) }}%</dd></dl></article>
      </section>
    </main>
  </div>
</template>
