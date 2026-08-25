<script setup lang="ts">
import {
  APIProblem,
  cancelAffiliateSupportRequest,
  enrollAffiliate,
  getAffiliateProgram,
  getAffiliateStatement,
  getAffiliateSupportRequests,
  submitAffiliateSupportRequest,
  type AffiliateProgram,
  type AffiliateStatement,
  type AffiliateSupportRequest
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { useSessionStore } from "../stores/session";

const route = useRoute();
const session = useSessionStore();
const program = ref<AffiliateProgram>();
const statement = ref<AffiliateStatement>();
const supportRequests = ref<AffiliateSupportRequest[]>([]);
const loading = ref(true);
const enrolling = ref(false);
const termsAccepted = ref(false);
const settlementAccountID = ref("");
const errorMessage = ref("");
const copied = ref(false);
const supportPending = ref("");
const ownerAccounts = computed(() => session.accounts.filter((account) => account.role === "owner"));
const enrollmentAvailable = computed(() => program.value?.enrollment_open && program.value.settlement_mode !== "unconfigured");
const codeShareable = computed(() => program.value?.enrollment?.state === "active" && program.value.attribution_enabled);
const openSupportRequests = computed(() => supportRequests.value.filter((request) => request.state === "submitted" || request.state === "in_review"));
const enrollmentAppealOpen = computed(() => openSupportRequests.value.some((request) => request.kind === "enrollment_appeal"));

function money(minor: number, currency: string): string {
  if (!currency) return "Not classified";
  return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(minor / 100);
}

function settlementLabel(mode: AffiliateProgram["settlement_mode"]): string {
  if (mode === "account_credit") return "Account billing credit";
  if (mode === "cash") return "Cash settlement";
  return "Not approved";
}

async function load(): Promise<void> {
  loading.value = true;
  errorMessage.value = "";
  try {
    program.value = await getAffiliateProgram();
    if (program.value.enrollment) {
      const [loadedStatement, loadedSupport] = await Promise.all([
        getAffiliateStatement(), getAffiliateSupportRequests()
      ]);
      statement.value = loadedStatement;
      supportRequests.value = [...loadedSupport.requests];
    }
  } catch (error) {
    errorMessage.value = error instanceof APIProblem ? error.message : "Affiliate details are temporarily unavailable.";
  } finally { loading.value = false; }
}

function supportLabel(request: AffiliateSupportRequest): string {
  if (request.state === "resolved") return "Approved";
  if (request.state === "declined") return "Not approved";
  if (request.state === "in_review") return "In review";
  if (request.state === "canceled") return "Canceled";
  return "Submitted";
}

function commissionReviewOpen(entryID: string): boolean {
  return openSupportRequests.value.some((request) => request.kind === "commission_review" && request.commission_entry_id === entryID);
}

async function submitSupport(kind: "enrollment_appeal" | "commission_review", commissionEntryID = ""): Promise<void> {
  if (supportPending.value) return;
  supportPending.value = commissionEntryID || kind;
  errorMessage.value = "";
  try {
    const request = await submitAffiliateSupportRequest({
      kind,
      ...(commissionEntryID ? { commission_entry_id: commissionEntryID } : {})
    });
    supportRequests.value = [request, ...supportRequests.value];
  } catch (error) {
    errorMessage.value = error instanceof APIProblem ? error.message : "The Affiliate review request could not be submitted.";
  } finally { supportPending.value = ""; }
}

async function cancelSupport(requestID: string): Promise<void> {
  if (supportPending.value) return;
  supportPending.value = requestID;
  errorMessage.value = "";
  try {
    const canceled = await cancelAffiliateSupportRequest(requestID);
    supportRequests.value = supportRequests.value.map((request) => request.request_id === requestID ? canceled : request);
  } catch (error) {
    errorMessage.value = error instanceof APIProblem ? error.message : "The Affiliate review request could not be canceled.";
  } finally { supportPending.value = ""; }
}

async function enroll(): Promise<void> {
  if (!program.value || !termsAccepted.value || !enrollmentAvailable.value || enrolling.value) return;
  if (program.value.settlement_mode === "account_credit" && !settlementAccountID.value) {
    errorMessage.value = "Choose an Account you own for billing-credit settlement.";
    return;
  }
  enrolling.value = true;
  errorMessage.value = "";
  try {
    program.value = await enrollAffiliate({
      accepted_terms_version: program.value.terms_version,
      ...(program.value.settlement_mode === "account_credit" ? { settlement_account_id: settlementAccountID.value } : {})
    });
    statement.value = await getAffiliateStatement();
    supportRequests.value = [...(await getAffiliateSupportRequests()).requests];
  } catch (error) {
    if (error instanceof APIProblem && error.problem?.code === "strong_reauthentication_required") {
      window.location.assign(`/app/security?return_to=${encodeURIComponent(route.fullPath)}&status=strong_reauthentication_required`);
      return;
    }
    errorMessage.value = error instanceof APIProblem ? error.message : "Affiliate enrollment could not be completed.";
  } finally { enrolling.value = false; }
}

async function copyCode(): Promise<void> {
  const code = program.value?.enrollment?.public_code;
  if (!code || !codeShareable.value) return;
  try {
    await navigator.clipboard.writeText(code);
    copied.value = true;
    window.setTimeout(() => { copied.value = false; }, 2000);
  } catch { errorMessage.value = "Copy is unavailable. Select the code and copy it manually."; }
}

onMounted(() => void load());
</script>

<template>
  <section class="page affiliate-page">
    <header class="page-heading"><p class="eyebrow">Affiliate</p><h1>One identity. One clear ledger.</h1><p>Your ordinary Infinite Ocean login holds the Affiliate enrollment. Customers actively enter your generated code at checkout; optional analytics never controls attribution or earnings.</p></header>
    <div v-if="loading" class="queue-state" role="status">Loading Affiliate program status…</div>
    <div v-else-if="errorMessage && !program" class="queue-state queue-state--error" role="alert"><h2>Affiliate details are unavailable</h2><p>{{ errorMessage }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></div>

    <template v-else-if="program?.enrollment">
      <section class="affiliate-code" aria-labelledby="affiliate-code-heading"><div><p class="eyebrow">Your generated code · {{ program.enrollment.state }}</p><h2 id="affiliate-code-heading">{{ program.enrollment.public_code }}</h2><p v-if="codeShareable">Share this code with a clear disclosure that you may earn recurring value from qualifying purchases. Customers choose whether to apply it in their checkout review.</p><p v-else-if="program.enrollment.state === 'suspended'">Referral attribution is paused for this enrollment. Do not promote the code while support reviews its status; historical commission records remain available below.</p><p v-else-if="program.enrollment.state === 'closed'">This enrollment is closed and the code cannot create new attribution. Historical commission records remain available below.</p><p v-else>New referral attribution is paused for the program. Do not promote the code until the program reopens; historical commission records remain available below.</p></div><div class="affiliate-code__actions"><IoButton kind="secondary" :disabled="!codeShareable" @click="copyCode">{{ copied ? "Copied" : "Copy code" }}</IoButton><IoButton v-if="program.enrollment.state === 'suspended' || program.enrollment.state === 'closed'" kind="secondary" :disabled="enrollmentAppealOpen || !!supportPending" @click="submitSupport('enrollment_appeal')">{{ enrollmentAppealOpen ? "Review requested" : "Request status review" }}</IoButton></div></section>
      <div class="affiliate-totals" aria-label="Commission totals"><article><small>Pending</small><strong>{{ money(statement?.pending_minor ?? 0, statement?.currency ?? '') }}</strong></article><article><small>Settled</small><strong>{{ money(statement?.settled_minor ?? 0, statement?.currency ?? '') }}</strong></article><article><small>Reversed</small><strong>{{ money(statement?.reversed_minor ?? 0, statement?.currency ?? '') }}</strong></article></div>
      <section class="affiliate-statement"><header><div><p class="eyebrow">Commission history</p><h2>Renewal ledger</h2></div><span>{{ settlementLabel(program.settlement_mode) }}</span></header><p v-if="!statement?.entries.length" class="form-note">No qualifying commission entries have been recorded. Referred customer identities and business details are never shown here.</p><ol v-else><li v-for="entry in statement.entries" :key="entry.entry_id"><div><strong>{{ entry.kind === 'reversal' ? 'Reversal' : `Qualifying cycle ${entry.cycle}` }}</strong><small>{{ new Date(entry.created_at).toLocaleDateString() }} · rule {{ entry.rule_version }}</small><button class="affiliate-review-link" type="button" :disabled="commissionReviewOpen(entry.entry_id) || !!supportPending" @click="submitSupport('commission_review', entry.entry_id)">{{ commissionReviewOpen(entry.entry_id) ? "Review requested" : "Request review" }}</button></div><span>{{ entry.kind === 'reversal' ? '−' : '' }}{{ money(entry.amount_minor, entry.currency) }}<small>{{ entry.state }}</small></span></li></ol></section>
      <section class="affiliate-support" aria-labelledby="affiliate-support-heading"><header><div><p class="eyebrow">Support</p><h2 id="affiliate-support-heading">Appeals and ledger reviews</h2></div></header><p class="form-note">Requests use only the enrollment or ledger entry already on this page. Do not send customer names, payment details, or referred-business information.</p><p v-if="!supportRequests.length" class="form-note">No review requests have been submitted.</p><ol v-else><li v-for="request in supportRequests" :key="request.request_id"><div><strong>{{ request.kind === 'enrollment_appeal' ? 'Enrollment status review' : 'Commission entry review' }}</strong><small>{{ new Date(request.created_at).toLocaleDateString() }}</small></div><div><span>{{ supportLabel(request) }}</span><button v-if="request.state === 'submitted'" class="affiliate-review-link" type="button" :disabled="!!supportPending" @click="cancelSupport(request.request_id)">Cancel</button></div></li></ol></section>
      <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
    </template>

    <section v-else-if="program" class="affiliate-enrollment">
      <p class="eyebrow">Enrollment</p><h2>Enable Affiliate status on this login</h2>
      <dl><div><dt>New enrollment</dt><dd>{{ program.enrollment_open ? "Open" : "Closed" }}</dd></div><div><dt>New checkout attribution</dt><dd>{{ program.attribution_enabled ? "Enabled" : "Disabled" }}</dd></div><div><dt>Settlement</dt><dd>{{ settlementLabel(program.settlement_mode) }}</dd></div><div><dt>Program rule</dt><dd>Version {{ program.rule_version }}</dd></div></dl>
      <div v-if="program.settlement_mode === 'unconfigured'" class="queue-inline-status queue-inline-status--error">Enrollment cannot open until the release owner approves whether qualifying commissions become Account credit or cash. The UI makes no payout promise before that decision.</div>
      <div v-else-if="!program.enrollment_open" class="queue-inline-status">Enrollment is currently closed. Existing commercial records remain available to enrolled Affiliates.</div>
      <template v-else>
        <label v-if="program.settlement_mode === 'account_credit'" for="settlement-account">Owned settlement Account</label><select v-if="program.settlement_mode === 'account_credit'" id="settlement-account" v-model="settlementAccountID"><option value="">Choose an Account</option><option v-for="account in ownerAccounts" :key="account.account_id" :value="account.account_id">{{ account.display_name }}</option></select>
        <label class="confirmation"><input v-model="termsAccepted" type="checkbox" /><span>I accept Affiliate terms version {{ program.terms_version }}, will clearly disclose the financial relationship, and understand that only qualifying paid invoices create ledger entries. <a href="https://www.infiniteocean.net/affiliate-terms" target="_blank" rel="noopener">Read terms</a>.</span></label>
        <IoButton :disabled="!termsAccepted || enrolling" @click="enroll">{{ enrolling ? "Enabling…" : "Enable Affiliate status" }}</IoButton>
      </template>
      <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
    </section>
  </section>
</template>
