<script setup lang="ts">
import {
  APIProblem, reauthenticateAdmin,
  createOperationsSupportGrant, logoutOperations, openOperationsSupportView,
  operationsLookup, operationsSession,
  operationsBillingFailures, operationsReplayBillingEvent, operationsRefreshBillingSubscription,
  operationsOpenPrivacyRights, operationsInspectPrivacyRight, operationsStartPrivacyReview, operationsResolvePrivacyRight,
  operationsInspectAffiliate, operationsInspectAffiliateRisk, operationsTransitionAffiliate,
  revokeOperationsSupportGrant,
  type OperationsAffiliateEnrollment, type OperationsAffiliateRiskEnvelope,
  type OperationsLookupKind,
  type OperationsLookupResult, type OperationsSession,
  type OperationsBillingFailuresReport, type OperationsPrivacyQueueItem, type OperationsPrivacyRequest,
  type OperationsSupportViewEnvelope
} from "@spyglass/api";
import { IoButton, IoLogo } from "@spyglass/design-system";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import AdminLogin from "./AdminLogin.vue";

import { availableModules, type ModuleID as Screen } from "./modules/registry";
const screen = ref<Screen>("overview");
const session = ref<OperationsSession>();
const loadingSession = ref(true);
const busy = ref(false);
const error = ref("");
const reauthNeeded = ref(false);
const reauthCode = ref("");
const menuOpen = ref(false);
const isDesktop = ref(typeof window !== "undefined" && window.innerWidth >= 1024);
const lookupKind = ref<OperationsLookupKind>("email");
const lookupValue = ref("");
const ticket = ref("");
const reason = ref("");
const lookupComplete = ref(false);
const lookupResults = ref<ReadonlyArray<OperationsLookupResult>>([]);
const supportView = ref<OperationsSupportViewEnvelope>();
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
const modules = computed(() => availableModules(roles.value));
const activeModule = computed(() => modules.value.find(module => module.id === screen.value));
const grantActive = computed(() => supportView.value?.view.grant.state === "active");
const noSubscription = computed(() => {
  const billing = supportView.value?.view.billing;
  return Boolean(billing && !billing.subscription_id && !billing.state);
});
const subscriptionStatus = computed(() => {
  const billing = supportView.value?.view.billing;
  return noSubscription.value ? "No subscription" : billing?.state?.replaceAll("_", " ") || "Status unavailable";
});
const navigationUnavailable = computed(() => !isDesktop.value && !menuOpen.value);

function message(value: unknown): string {
  if (value instanceof APIProblem) { if (value.problem?.code === "admin_reauthentication_required") reauthNeeded.value = true; return value.message; }
  if (value instanceof Error) return value.message;
  return "The request could not be completed.";
}

async function loadSession(): Promise<void> {
  loadingSession.value = true;
  try { session.value = await operationsSession(); restoreModule(); }
  catch (value) { session.value = undefined; if (!(value instanceof APIProblem && value.status === 401)) error.value = message(value); }
  finally { loadingSession.value = false; }
}

function signedIn(value: OperationsSession): void { session.value = value; restoreModule(); error.value = ""; }
async function confirmIdentity(): Promise<void> {
  busy.value = true; error.value = "";
  try { await reauthenticateAdmin(reauthCode.value); reauthNeeded.value = false; reauthCode.value = ""; }
  catch (value) { error.value = message(value); }
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
  if (!modules.value.some(module => module.id === destination)) return;
  screen.value = destination; window.location.hash = destination; menuOpen.value = false; error.value = "";
}

function restoreModule(): void {
  const destination = window.location.hash.slice(1);
  screen.value = modules.value.find(module => module.id === destination)?.id ?? "overview";
}

async function lookupFromDirectory(input: { kind: "user_id" | "account_id"; value: string; ticket: string; reason: string }): Promise<void> {
 lookupKind.value = input.kind; lookupValue.value = input.value; ticket.value = input.ticket; reason.value = input.reason;
 navigate("lookup");
 await findAccount();
}

async function findAccount(): Promise<void> {
  busy.value = true; error.value = ""; lookupResults.value = []; lookupComplete.value = false;
  try {
    const response = await operationsLookup({ kind: lookupKind.value, value: lookupValue.value.trim(), ticket: ticket.value.trim(), reason: reason.value.trim() });
    lookupResults.value = response.results; lookupComplete.value = true;
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
onMounted(() => { window.addEventListener("resize", trackViewport); window.addEventListener("hashchange", restoreModule); void loadSession(); });
onBeforeUnmount(() => { window.removeEventListener("resize", trackViewport); window.removeEventListener("hashchange", restoreModule); });
</script>

<template>
  <div v-if="loadingSession" class="centered" aria-live="polite">Checking staff session…</div>
  <AdminLogin v-else-if="!session" @signed-in="signedIn" />
  <div v-else class="shell">
    <header class="topbar">
      <button class="menu-button" type="button" :aria-expanded="menuOpen" aria-controls="operations-navigation" @click="menuOpen = !menuOpen">Menu</button>
      <IoLogo />
      <span class="staff-name">{{ session.staff.display_name }}</span>
    </header>
    <aside id="operations-navigation" class="sidebar" :class="{ 'sidebar--open': menuOpen }" :inert="navigationUnavailable" :aria-hidden="navigationUnavailable || undefined">
      <IoLogo />
      <nav aria-label="Operations navigation">
        <button v-for="module in modules" :key="module.id" type="button" :aria-current="screen === module.id ? 'page' : undefined" @click="navigate(module.id)">{{ module.label }}</button>
      </nav>
      <div class="sidebar-footer">
        <p>{{ session.staff.display_name }}</p>
        <small>{{ roles.map(role => role === 'operations_administrator' ? 'Administrator' : role.charAt(0).toUpperCase() + role.slice(1)).join(" · ") }}</small>
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
      <form v-if="reauthNeeded" class="form-card" @submit.prevent="confirmIdentity"><h2>Confirm it is you</h2><label>Authenticator code<input v-model="reauthCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required /></label><IoButton type="submit" :disabled="busy">Confirm</IoButton><p>After confirming, repeat the action you were taking.</p></form>
      <section v-if="screen === 'overview'" aria-labelledby="overview-title">
        <p class="eyebrow">Operations</p>
        <h1 id="overview-title">Admin overview</h1>
        <p class="lede">Choose a tool below. Reports and customer access are recorded under your staff account.</p>
        <div class="action-grid">
          <button v-for="module in modules.filter(item => item.id !== 'overview')" :key="module.id" type="button" class="action-card" @click="navigate(module.id)"><strong>{{ module.label }}</strong><span>{{ module.description }}</span></button>
        </div>
        <section class="guardrail-card" aria-labelledby="guardrails-title">
          <h2 id="guardrails-title">Built-in guardrails</h2>
          <ul><li>User and team lists are administrator-only.</li><li>No customer mutations while viewing an account.</li><li>Every lookup, grant, view, and revocation is recorded.</li></ul>
        </section>
      </section>

      <component :is="activeModule.component" v-else-if="activeModule?.component" :key="screen" @lookup="lookupFromDirectory" />

      <section v-else-if="screen === 'lookup'" aria-labelledby="lookup-title">
        <p class="eyebrow">Support</p><h1 id="lookup-title">Find one customer</h1>
        <p class="lede">Use an exact identifier from the support request. A ticket and plain-language reason are required.</p>
        <form class="form-card" @submit.prevent="findAccount">
          <label>Look up by<select v-model="lookupKind"><option value="email">Email</option><option value="user_id">User ID</option><option value="account_id">Account ID</option><option value="stripe_customer_id">Stripe customer ID</option><option value="stripe_subscription_id">Stripe subscription ID</option></select></label>
          <label>Exact value<input v-model="lookupValue" required autocomplete="off" /></label>
          <div class="form-row"><label>Support ticket<input v-model="ticket" required placeholder="SUP-1042" autocomplete="off" /></label><label>Reason<input v-model="reason" required placeholder="Customer asked us to check billing access" autocomplete="off" /></label></div>
          <IoButton type="submit" :disabled="busy">{{ busy ? "Searching…" : "Find exact match" }}</IoButton>
        </form>
        <p v-if="lookupComplete && lookupResults.length === 0 && !busy" class="empty-state">No team membership found for this identifier. A registered user or team can exist without a current membership.</p>
        <div v-else class="result-list">
          <article v-for="result in lookupResults" :key="`${result.user_id}:${result.account_id}`" class="result-card">
            <div><h2>{{ result.account_name }}</h2><p>{{ result.display_name }} · {{ result.email }}</p><small>{{ result.membership_role }} · Account {{ result.account_state }} · User {{ result.user_state }}</small></div>
            <IoButton :disabled="busy" @click="inspectAccount(result)">Open 15-minute view</IoButton>
          </article>
        </div>
        <section v-if="supportView" class="account-view" aria-labelledby="account-view-title">
          <div class="section-heading"><div><p class="eyebrow">Read only</p><h2 id="account-view-title">{{ supportView.view.account.display_name }}</h2></div><span class="state-pill">{{ supportView.view.account.state === "active" ? "Team open" : "Team " + supportView.view.account.state }}</span></div>
          <div class="detail-grid">
            <article><h3>Customer</h3><dl><dt>Name</dt><dd>{{ supportView.view.user.display_name }}</dd><dt>Email</dt><dd>{{ supportView.view.user.email }}</dd><dt>Email verified</dt><dd>{{ formatDate(supportView.view.user.email_verified_at) }}</dd><dt>Passkeys</dt><dd>{{ supportView.view.user.passkey_count }}</dd><dt>Membership</dt><dd>{{ supportView.view.membership.role }} · {{ supportView.view.membership.state }}</dd></dl></article>
            <article><h3>Subscription</h3><dl><dt>Status</dt><dd>{{ subscriptionStatus }}</dd><dt>Plan</dt><dd>{{ supportView.view.billing.offer_code || "Not set" }}</dd><dt>Period ends</dt><dd>{{ formatDate(supportView.view.billing.current_period_end) }}</dd><dt>Cancel at</dt><dd>{{ formatDate(supportView.view.billing.cancel_at) }}</dd><dt>Stripe customer</dt><dd>{{ supportView.view.billing.customer_id || "Not set" }}</dd></dl></article>
            <article>
              <h3>AI tokens</h3>
              <dl><dt>Available</dt><dd>{{ supportView.view.ai_tokens.available.toLocaleString() }}</dd><dt>Used</dt><dd>{{ supportView.view.ai_tokens.consumed.toLocaleString() }}</dd><dt>Set aside for AI work</dt><dd>{{ supportView.view.ai_tokens.reserved.toLocaleString() }}</dd></dl>
              <p v-if="supportView.view.ai_tokens.available === 0" class="account-help">No AI tokens available for new AI work.</p>
              <p v-if="noSubscription && supportView.view.account.type === 'inactive' && supportView.view.ai_tokens.available === 0 && supportView.view.ai_tokens.reserved === 0" class="account-help">Signing up does not add AI tokens. The included allowance is added after a verified subscription payment.</p>
              <p v-if="supportView.view.ai_tokens.reserved > 0" class="account-help">Tokens set aside for AI work are unavailable until that work finishes or releases them.</p>
            </article>
            <article><h3>Lifecycle</h3><dl><dt>Status</dt><dd>{{ supportView.view.lifecycle.state || "No active request" }}</dd><dt>Execute after</dt><dd>{{ formatDate(supportView.view.lifecycle.execute_after) }}</dd><dt>Delete after</dt><dd>{{ formatDate(supportView.view.lifecycle.delete_after) }}</dd><dt>Blocker</dt><dd>{{ supportView.view.lifecycle.blocker_code || "None" }}</dd></dl></article>
          </div>
          <details class="account-technical"><summary>Technical details</summary><dl><dt>Entitlement version</dt><dd>{{ supportView.view.entitlements.version }}</dd><dt>Catalog version</dt><dd>{{ supportView.view.entitlements.catalog_version }}</dd></dl></details>
          <article class="history-card"><h3>Support history visible to the customer</h3><p v-if="supportView.view.support_history.length === 0">No prior support access.</p><ol v-else><li v-for="event in supportView.view.support_history" :key="event.id"><strong>{{ event.action }}</strong> by {{ event.staff_display_name }} · {{ event.ticket }}<br /><span>{{ event.reason }} · {{ formatDate(event.occurred_at) }}</span></li></ol></article>
        </section>
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
