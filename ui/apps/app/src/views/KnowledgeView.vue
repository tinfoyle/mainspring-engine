<script setup lang="ts">
import {
  APIProblem,
  decideKnowledgeClaim,
  getKnowledgeClaim,
  listKnowledgeFacts,
  listProposedKnowledgeClaims,
  type KnowledgeClaim,
  type KnowledgeClaimSummary,
  type KnowledgeFactSummary,
  type KnowledgeValue
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const route = useRoute();
const claims = ref<ReadonlyArray<KnowledgeClaimSummary>>([]);
const facts = ref<ReadonlyArray<KnowledgeFactSummary>>([]);
const detail = ref<KnowledgeClaim>();
const loading = ref(false);
const detailLoading = ref(false);
const saving = ref(false);
const error = ref("");
const detailError = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const decision = ref<"accept" | "reject">("accept");
const reason = ref("");
let queueSequence = 0;
let detailSequence = 0;

const knowledgePackage = computed(() => session.selected?.entitlements.packages.find((value) => value.code === "knowledge"));
const available = computed(() => Boolean(knowledgePackage.value && knowledgePackage.value.mode !== "suspended"));
const writable = computed(() => Boolean(knowledgePackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected.role)));
const claimID = computed(() => typeof route.params.claimID === "string" ? route.params.claimID : "");
const hasDecisionDraft = computed(() => Boolean(
  detail.value?.state === "proposed"
  && writable.value
  && (decision.value !== "accept" || reason.value.trim())
));

useSafeNavigation({
  dirty: hasDecisionDraft,
  pending: saving,
  message: "Leave this Knowledge decision? Your unsubmitted reason will remain only on this page.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Knowledge decision is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Knowledge decision remains ready for review.";
  }
});

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function factTitle(key: string): string {
  if (/^agent\.owner_question[._]/.test(key)) return "Answer to an agent’s question";
  const names: Record<string, string> = {
    "baseline.pain_point": "Main problem", "baseline.revenue_workflow": "How customers pay",
    "baseline.customer_onboarding": "Getting customers started", "baseline.immediate_concern": "Current priority",
    "organization.website_url": "Website"
  };
  return names[key] ?? label(key.replace(/^(baseline|organization)\./, "").replaceAll(".", " "));
}
function scope(value: { readonly kind: string; readonly id?: string }): string { return value.id ? `${label(value.kind)} · ${value.id}` : label(value.kind); }
function formatValue(value: KnowledgeValue): string { return typeof value === "string" ? value : JSON.stringify(value, null, 2); }
function date(value: string): string { return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value)); }

async function refresh(announce = false): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !available.value) { claims.value = []; facts.value = []; return; }
  const sequence = ++queueSequence;
  loading.value = true; error.value = "";
  try {
    const [claimItems, factItems] = await Promise.all([listProposedKnowledgeClaims(accountID), listKnowledgeFacts(accountID)]);
    if (sequence !== queueSequence) return;
    claims.value = claimItems; facts.value = factItems;
    if (announce) announcement.value = `Knowledge refreshed. ${claimItems.length} proposed ${claimItems.length === 1 ? "claim" : "claims"} need review.`;
  } catch (cause) { if (sequence === queueSequence) error.value = cause instanceof APIProblem ? cause.message : "Knowledge is unavailable right now."; }
  finally { if (sequence === queueSequence) loading.value = false; }
}

async function loadDetail(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !claimID.value || !available.value) { detail.value = undefined; return; }
  const sequence = ++detailSequence;
  detailLoading.value = true; detailError.value = "";
  try { const value = await getKnowledgeClaim(accountID, claimID.value); if (sequence === detailSequence) detail.value = value; }
  catch (cause) { if (sequence === detailSequence) detailError.value = cause instanceof APIProblem ? cause.message : "This claim is unavailable right now."; }
  finally { if (sequence === detailSequence) detailLoading.value = false; }
}

async function submitDecision(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !detail.value || saving.value) return;
  saving.value = true; detailError.value = ""; navigationNotice.value = "";
  try {
    const result = await decideKnowledgeClaim(accountID, detail.value, { accept: decision.value === "accept", reason: reason.value.trim() });
    detail.value = result.claim;
    reason.value = "";
    announcement.value = `Claim ${decision.value === "accept" ? "accepted" : "rejected"}.`;
    await refresh();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 412) { await loadDetail(); detailError.value = "This claim changed. Review the current version before deciding again."; }
    else detailError.value = cause instanceof APIProblem ? cause.message : "The claim decision could not be saved.";
  } finally { saving.value = false; }
}

watch(() => [session.selectedID, available.value], () => void refresh(), { immediate: true });
watch(() => [session.selectedID, claimID.value, available.value], () => void loadDetail(), { immediate: true });
</script>

<template>
  <section class="page knowledge-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <template v-if="claimID">
      <RouterLink class="back-link" to="/app/knowledge">← Back to Knowledge</RouterLink>
      <section v-if="detailLoading" class="queue-state" role="status"><h1>Loading information…</h1></section>
      <section v-else-if="detailError && !detail" class="queue-state queue-state--error" role="alert"><h1>Information did not load</h1><p>{{ detailError }}</p><IoButton kind="secondary" @click="loadDetail">Try again</IoButton></section>
      <template v-else-if="detail">
        <header class="detail-heading"><div><p class="eyebrow">{{ scope(detail.scope) }} information</p><h1>{{ factTitle(detail.key) }}</h1></div><span class="state-badge">{{ label(detail.state) }}</span></header>
        <p v-if="detailError" class="queue-inline-status queue-inline-status--error" role="alert">{{ detailError }}</p>
        <div class="detail-layout">
          <article class="detail-card knowledge-detail-card"><h2>Information</h2><pre class="knowledge-value">{{ formatValue(detail.value) }}</pre><dl><div><dt>Confidence</dt><dd>{{ detail.confidence / 10 }}%</dd></div><div><dt>Sensitivity</dt><dd>{{ label(detail.sensitivity) }}</dd></div><div><dt>Version</dt><dd>{{ detail.version }}</dd></div></dl><section class="knowledge-citations"><h2>Citations</h2><ol><li v-for="citation in detail.citations" :key="`${citation.evidence_id}:${citation.locator}`"><strong>{{ label(citation.relation) }} · {{ label(citation.evidence_kind) }}</strong><span>{{ citation.locator }}</span></li></ol></section></article>
          <form class="decision-card" @submit.prevent="submitDecision"><h2>Review this information</h2><p v-if="detail.state !== 'proposed'" class="form-note">This information has already been reviewed.</p><p v-else-if="!writable" class="form-note">Your current package or role provides read-only access.</p><template v-else><fieldset><legend>Decision</legend><label><input v-model="decision" type="radio" value="accept"> Save this as confirmed information</label><label><input v-model="decision" type="radio" value="reject"> Reject this proposal</label></fieldset><label>Reason<textarea v-model="reason" minlength="3" maxlength="1000" rows="5" required></textarea></label><p class="form-note">Check the information and its sources before saving it.</p><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : decision === "accept" ? "Confirm information" : "Reject suggestion" }}</IoButton></template></form>
        </div>
      </template>
    </template>

    <template v-else>
      <header class="page-heading page-heading--action"><div><h1>Knowledge</h1><p>Read saved business information and review new suggestions.</p></div><IoButton kind="secondary" :disabled="loading || !available" @click="refresh(true)">{{ loading ? "Refreshing…" : "Refresh" }}</IoButton></header>
      <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Knowledge is always isolated to one Account.</p></section>
      <section v-else-if="!available" class="queue-state"><h2>Knowledge is not enabled</h2><p>Knowledge is not included in this account’s plan.</p><a href="/app/billing">Review Account plans</a></section>
      <template v-else>
        <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Finish two-factor authentication before deciding claims.</p><a href="/app/security?return_to=%2Fapp%2Fknowledge">Continue security setup</a></section>
        <p v-if="error && (claims.length || facts.length)" class="queue-inline-status queue-inline-status--error">{{ error }} Showing the last saved information.</p>
        <section class="knowledge-section"><header><div><h2>Needs review</h2></div><span>{{ claims.length }} waiting</span></header><div v-if="loading && claims.length === 0" class="queue-state" role="status">Loading proposed claims…</div><div v-else-if="error && claims.length === 0" class="queue-state queue-state--error" role="alert"><h3>Knowledge did not load</h3><p>{{ error }}</p></div><div v-else-if="claims.length === 0" class="queue-state"><h3>Nothing needs review</h3><p>New suggestions will appear here for you to check.</p></div><ol v-else class="knowledge-list"><li v-for="claim in claims" :key="claim.id"><RouterLink :to="`/app/knowledge/claims/${claim.id}`" class="knowledge-card"><strong>{{ factTitle(claim.key) }}</strong><span>{{ scope(claim.scope) }} · {{ claim.confidence / 10 }}% confidence</span><small>{{ label(claim.sensitivity) }} · version {{ claim.version }}</small></RouterLink></li></ol></section>
        <section class="knowledge-section"><header><div><h2>Saved information</h2></div><span>{{ facts.length }} current</span></header><div v-if="facts.length === 0 && !loading" class="queue-state"><h3>No saved information yet</h3><p>Confirmed business information will appear here.</p></div><ol v-else class="knowledge-list knowledge-list--facts"><li v-for="fact in facts" :key="fact.id"><RouterLink :to="`/app/knowledge/claims/${fact.current_claim_id}`" class="knowledge-card"><strong>{{ factTitle(fact.key) }}</strong><span>{{ scope(fact.scope) }} · revision {{ fact.revision }}</span><small>{{ label(fact.sensitivity) }} · accepted {{ date(fact.accepted_at) }}</small></RouterLink></li></ol></section>
      </template>
    </template>
  </section>
</template>
