<script setup lang="ts">
import {
  answerBaseline, approveBaselinePlan, beginBaselineInventory, captureOwnerKnowledgeFact, completeBaselineInventory,
  confirmBaselineWorkEvidence, createBaselineSourceGrant, decideBaselineEvidence, dispositionBaselineRequirement,
  getBaseline, getCurrentBaseline, getIntegrationConnection, isAPIProblem, listBaselineSourceGrants, listIntegrationConnections,
  listKnowledgeFacts, listWork, markBaselineReady, materializeBaselineMaintenance, materializeBaselinePlan,
  reassessBaseline, revokeBaselineSourceGrant, startBaseline, submitBaselinePlan,
  type BaselineAssessment, type BaselineRequirement, type BaselineSourceGrant, type IntegrationConnection,
  type IntegrationConnectionDetail, type KnowledgeFactSummary, type WorkItem
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const questions: ReadonlyArray<{ readonly key: string; readonly prompt: string; readonly explanation: string; readonly optional?: boolean }> = [
  { key: "organization.legal_name", prompt: "What is the legal or registered name of the business?", explanation: "This anchors the assessment to the correct business." },
  { key: "organization.website_url", prompt: "What is the business website?", explanation: "A public website gives Spyglass a safe starting point.", optional: true },
  { key: "organization.industry", prompt: "What trade, industry or business model best describes the company?", explanation: "Your industry changes which records and controls apply." },
  { key: "organization.primary_location", prompt: "Where does the business primarily operate?", explanation: "Jurisdiction affects licensing, employment, tax and insurance evidence." },
  { key: "organization.services", prompt: "What products or services produce revenue today?", explanation: "This keeps recommendations grounded in the way the business earns money." },
  { key: "organization.team_size", prompt: "How many people work in the business, including owners and regular contractors?", explanation: "Team size changes the workforce controls a business reasonably needs." },
  { key: "baseline.immediate_concern", prompt: "What uncertainty or operating problem should the first plan prioritize?", explanation: "The first plan should reflect your immediate concern." }
];
const stages = ["interview", "inventory", "gap_review", "plan_approval", "active", "ready"] as const;
const session = useSessionStore(); const route = useRoute(); const router = useRouter();
const baseline = ref<BaselineAssessment>(); const facts = ref<ReadonlyArray<KnowledgeFactSummary>>([]); const work = ref<ReadonlyArray<WorkItem>>([]);
const connections = ref<ReadonlyArray<IntegrationConnection>>([]); const selectedConnection = ref<IntegrationConnectionDetail>(); const grants = ref<ReadonlyArray<BaselineSourceGrant>>([]);
const loading = ref(false); const saving = ref(false); const error = ref(""); const announcement = ref(""); const navigationNotice = ref(""); let sequence = 0;
const skipped = ref(new Set<string>()); const answerKind = ref<"fact" | "statement" | "unknown">("statement"); const factID = ref(""); const answerValue = ref(""); const reason = ref("");
const requirementID = ref(""); const evidenceID = ref(""); const disposition = ref<"accepted" | "rejected" | "gap" | "not_applicable">("gap"); const reviewReason = ref("");
const workItemID = ref(""); const connectionID = ref(""); const folders = ref(""); const sinceAt = ref(""); const untilAt = ref(""); const revokeReason = ref("");

const knowledgePackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "knowledge"));
const integrationPackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "integrations"));
const available = computed(() => Boolean(knowledgePackage.value && knowledgePackage.value.mode !== "suspended"));
const writable = computed(() => knowledgePackage.value?.mode === "enabled" && ["owner", "administrator", "member"].includes(session.selected?.role ?? ""));
const manageable = computed(() => knowledgePackage.value?.mode === "enabled" && ["owner", "administrator"].includes(session.selected?.role ?? ""));
const sourcesManageable = computed(() => integrationPackage.value?.mode === "enabled" && ["owner", "administrator"].includes(session.selected?.role ?? ""));
const currentStage = computed(() => Math.max(0, stages.indexOf((baseline.value?.state === "archived" ? "ready" : baseline.value?.state ?? "interview") as typeof stages[number])));
const answeredKeys = computed(() => new Set(baseline.value?.answers.map((item) => item.question_key) ?? []));
const question = computed(() => questions.find((item) => !answeredKeys.value.has(item.key) && !skipped.value.has(item.key)));
const matchingFacts = computed(() => facts.value.filter((item) => item.key === question.value?.key && item.state === "active"));
const requiredComplete = computed(() => questions.filter((item) => !("optional" in item)).every((item) => answeredKeys.value.has(item.key)));
const pendingRequirement = computed(() => baseline.value?.requirements.find((item) => item.disposition === "pending"));
const gaps = computed(() => baseline.value?.requirements.filter((item) => item.disposition === "gap") ?? []);
const reviewedCount = computed(() => baseline.value?.requirements.filter((item) => item.disposition !== "pending").length ?? 0);
const linkedWork = computed(() => work.value.filter((item) => item.provenance.source === "baseline" && gaps.value.some((gap) => gap.id === item.provenance.baseline_requirement_id)));
const planMaterialized = computed(() => gaps.value.every((gap) => linkedWork.value.some((item) => item.provenance.baseline_requirement_id === gap.id)));
const readyForReady = computed(() => baseline.value?.requirements.every((item) => ["satisfied", "not_applicable"].includes(item.disposition)) ?? false);
const sourceConnections = computed(() => connections.value.filter((item) => item.state === "active" && ["email", "google_drive"].includes(item.kind)));
const hasUnsavedBaselineWork = computed(() => {
  if (!baseline.value) return false;
  if (baseline.value.state === "interview") {
    return Boolean(skipped.value.size || answerKind.value !== "statement" || factID.value || answerValue.value.trim() || reason.value.trim());
  }
  if (baseline.value.state === "gap_review") {
    return Boolean(disposition.value !== "gap" || evidenceID.value || reviewReason.value.trim());
  }
  if (["active", "ready"].includes(baseline.value.state)) {
    return Boolean(
      workItemID.value
      || evidenceID.value
      || reviewReason.value.trim()
      || connectionID.value
      || folders.value.trim()
      || sinceAt.value
      || untilAt.value
      || revokeReason.value.trim()
    );
  }
  return false;
});

const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedBaselineWork,
  pending: saving,
  message: "Leave Business Baseline? Your unsubmitted answer or review will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Baseline change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Baseline answer or review remains on this page.";
  }
});

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value)) : "Not set"; }
function planInput(value: BaselineAssessment) { if (!value.plan) throw new Error("The frozen plan is unavailable."); return { plan_id: value.plan.id, content_sha256: value.plan.content_sha256, assessment_version: value.plan.assessment_version }; }
function resetReview(value?: BaselineRequirement): void { requirementID.value = value?.id ?? ""; evidenceID.value = ""; disposition.value = "gap"; reviewReason.value = ""; }
function handle(operation: string, caught: unknown): void { error.value = caught instanceof Error ? caught.message : `Could not ${operation}.`; }
async function reloadSupporting(accountID: string): Promise<void> {
  const results = await Promise.allSettled([listKnowledgeFacts(accountID), listWork(accountID, { limit: 100 }), listIntegrationConnections(accountID, { state: "active" })]);
  facts.value = results[0].status === "fulfilled" ? results[0].value : []; work.value = results[1].status === "fulfilled" ? results[1].value.items : []; connections.value = results[2].status === "fulfilled" ? results[2].value.items : [];
}
async function load(): Promise<void> {
  const accountID = session.selectedID; const current = ++sequence; baseline.value = undefined; facts.value = []; work.value = []; connections.value = []; grants.value = []; error.value = "";
  if (!accountID || !available.value) return; loading.value = true;
  try {
    const id = typeof route.params.assessmentID === "string" ? route.params.assessmentID : "";
    try { baseline.value = id ? await getBaseline(accountID, id) : await getCurrentBaseline(accountID); }
    catch (caught) { if (!isAPIProblem(caught) || caught.status !== 404 || id) throw caught; }
    if (current !== sequence) return;
    if (baseline.value && !id) await router.replace(`/app/baseline/${baseline.value.id}`);
    await reloadSupporting(accountID);
    if (baseline.value && ["active", "ready"].includes(baseline.value.state) && integrationPackage.value) grants.value = (await listBaselineSourceGrants(accountID, baseline.value.id)).items;
    resetReview(pendingRequirement.value);
  } catch (caught) { handle("load Baseline", caught); } finally { if (current === sequence) loading.value = false; }
}
async function start(): Promise<void> { const accountID = session.selectedID; if (!accountID) return; saving.value = true; error.value = ""; navigationNotice.value = ""; try { baseline.value = await startBaseline(accountID); allowNextNavigation(); await router.replace(`/app/baseline/${baseline.value.id}`); announcement.value = "Business Baseline started."; } catch (caught) { handle("start Baseline", caught); } finally { saving.value = false; } }
async function saveAnswer(): Promise<void> {
  const accountID = session.selectedID; const value = baseline.value; const item = question.value; if (!accountID || !value || !item) return; saving.value = true; error.value = ""; navigationNotice.value = "";
  try {
    let selected = facts.value.find((entry) => entry.id === factID.value);
    if (answerKind.value === "statement") selected = await captureOwnerKnowledgeFact(accountID, item.key, answerValue.value, `baseline/${value.id}/${item.key}`);
    baseline.value = await answerBaseline(accountID, value, answerKind.value === "unknown" ? { question_key: item.key, kind: "unknown", reason: reason.value } : { question_key: item.key, kind: "fact", fact: { fact_id: selected?.id ?? "", revision: selected?.revision ?? 0 } });
    factID.value = ""; answerValue.value = ""; reason.value = ""; answerKind.value = "statement"; announcement.value = "Answer confirmed and saved.";
  }
  catch (caught) { handle("save answer", caught); } finally { saving.value = false; }
}
async function advance(kind: "inventory" | "complete" | "submit" | "approve" | "materialize" | "ready" | "maintenance" | "reassess"): Promise<void> {
  const accountID = session.selectedID; const value = baseline.value; if (!accountID || !value) return; saving.value = true; error.value = ""; navigationNotice.value = "";
  try {
    if (kind === "inventory") baseline.value = await beginBaselineInventory(accountID, value);
    else if (kind === "complete") baseline.value = await completeBaselineInventory(accountID, value);
    else if (kind === "submit") baseline.value = await submitBaselinePlan(accountID, value);
    else if (kind === "approve") baseline.value = await approveBaselinePlan(accountID, value, planInput(value));
    else if (kind === "materialize") { const page = await materializeBaselinePlan(accountID, value, planInput(value)); work.value = [...work.value, ...page.items]; }
    else if (kind === "ready") baseline.value = await markBaselineReady(accountID, value);
    else if (kind === "maintenance") { const page = await materializeBaselineMaintenance(accountID, value); work.value = [...work.value, ...page.items]; }
    else { const result = await reassessBaseline(accountID, value); baseline.value = result.next; allowNextNavigation(); await router.replace(`/app/baseline/${result.next.id}`); }
    resetReview(pendingRequirement.value); announcement.value = kind === "reassess" ? "Reassessment started." : "Baseline updated.";
  } catch (caught) { handle("update Baseline", caught); } finally { saving.value = false; }
}
async function review(): Promise<void> {
  const accountID = session.selectedID; const value = baseline.value; if (!accountID || !value || !requirementID.value) return; saving.value = true; error.value = ""; navigationNotice.value = "";
  try { baseline.value = ["accepted", "rejected"].includes(disposition.value) ? await decideBaselineEvidence(accountID, value, { requirement_id: requirementID.value, evidence_id: evidenceID.value, decision: disposition.value as "accepted" | "rejected", reason: reviewReason.value }) : await dispositionBaselineRequirement(accountID, value, { requirement_id: requirementID.value, disposition: disposition.value as "gap" | "not_applicable", reason: reviewReason.value }); resetReview(pendingRequirement.value); announcement.value = "Requirement reviewed."; }
  catch (caught) { handle("review requirement", caught); } finally { saving.value = false; }
}
async function confirmWork(): Promise<void> {
  const accountID = session.selectedID; const value = baseline.value; const item = work.value.find((entry) => entry.id === workItemID.value); if (!accountID || !value || !item?.provenance.baseline_requirement_id) return; saving.value = true; error.value = ""; navigationNotice.value = "";
  try { baseline.value = await confirmBaselineWorkEvidence(accountID, value, { requirement_id: item.provenance.baseline_requirement_id, work_item_id: item.id, evidence_id: evidenceID.value, reason: reviewReason.value }); workItemID.value = ""; evidenceID.value = ""; reviewReason.value = ""; announcement.value = "Completed Work evidence confirmed."; }
  catch (caught) { handle("confirm Work evidence", caught); } finally { saving.value = false; }
}
async function selectConnection(): Promise<void> { const accountID = session.selectedID; if (!accountID || !connectionID.value) { selectedConnection.value = undefined; return; } try { selectedConnection.value = await getIntegrationConnection(accountID, connectionID.value); const scoped = selectedConnection.value.revision.scope.drive_folder_ids; folders.value = scoped?.join("\n") ?? (selectedConnection.value.connection.kind === "email" ? "INBOX" : ""); } catch (caught) { handle("load connection scope", caught); } }
async function grantSource(): Promise<void> { const accountID = session.selectedID; const value = baseline.value; const connection = selectedConnection.value; if (!accountID || !value || !connection) return; saving.value = true; error.value = ""; navigationNotice.value = ""; try { const grant = await createBaselineSourceGrant(accountID, value.id, { connection_id: connection.connection.id, source_kind: connection.connection.kind as "email" | "google_drive", folders: folders.value.split("\n").map((item) => item.trim()).filter(Boolean), ...(sinceAt.value ? { since_at: new Date(sinceAt.value).toISOString() } : {}), ...(untilAt.value ? { until_at: new Date(untilAt.value).toISOString() } : {}) }); grants.value = [...grants.value, grant]; connectionID.value = ""; selectedConnection.value = undefined; folders.value = ""; sinceAt.value = ""; untilAt.value = ""; announcement.value = "Read-only source granted."; } catch (caught) { handle("grant source", caught); } finally { saving.value = false; } }
async function revoke(grant: BaselineSourceGrant): Promise<void> { const accountID = session.selectedID; const value = baseline.value; if (!accountID || !value) return; saving.value = true; error.value = ""; navigationNotice.value = ""; try { const updated = await revokeBaselineSourceGrant(accountID, value.id, grant, { reason: revokeReason.value }); grants.value = grants.value.map((item) => item.id === updated.id ? updated : item); revokeReason.value = ""; announcement.value = "Source access revoked."; } catch (caught) { handle("revoke source", caught); } finally { saving.value = false; } }

watch([() => session.selectedID, () => route.params.assessmentID], load, { immediate: true });
</script>

<template>
  <section class="page baseline-page">
    <header class="page-heading"><p class="eyebrow">Guided business setup</p><h1>Business Baseline</h1><p>Turn what you know today into a reviewed operating plan. Every answer, evidence decision and plan approval stays attributable.</p></header>
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <div v-if="loading" class="queue-state"><h2>Loading your Baseline…</h2></div>
    <div v-else-if="!available" class="queue-state queue-state--warning"><h2>Knowledge is not available</h2><p>Your selected Account does not currently include readable Knowledge access.</p></div>
    <div v-else-if="error && !baseline" class="queue-state queue-state--error"><h2>Baseline could not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></div>
    <section v-else-if="!baseline" class="baseline-welcome"><div><p class="eyebrow">About 10–15 minutes</p><h2>Build the operating picture Spyglass will use</h2><p>You will connect existing confirmed facts, review the evidence your business should maintain, and approve the exact Work plan. “I don’t know yet” is a valid answer—it becomes visible work instead of a guess.</p><ul><li>Nothing is accepted as fact without a reviewed Knowledge record.</li><li>Agents cannot approve your plan or decide evidence.</li><li>You can leave and resume on any device.</li></ul></div><IoButton v-if="manageable" :disabled="saving" @click="start">{{ saving ? "Starting…" : "Start my Baseline" }}</IoButton><p v-else class="queue-inline-status">An Owner or Administrator must start the first Baseline.</p></section>
    <template v-else>
      <nav class="baseline-progress" aria-label="Baseline progress" tabindex="0"><ol><li v-for="(stage, index) in stages" :key="stage" :class="{ done: index < currentStage, current: index === currentStage }"><span>{{ index + 1 }}</span><small>{{ label(stage) }}</small></li></ol></nav>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section v-if="baseline.state === 'interview'" class="baseline-focus-card">
        <template v-if="question"><div class="baseline-card-heading"><span>Question {{ answeredKeys.size + 1 }} of {{ questions.length }}</span><strong>{{ Math.round((answeredKeys.size / questions.length) * 100) }}% captured</strong></div><h2>{{ question.prompt }}</h2><p>{{ question.explanation }}</p><form class="baseline-form" @submit.prevent="saveAnswer"><fieldset><legend>How would you like to answer?</legend><label><input v-model="answerKind" type="radio" value="statement">Confirm my answer now</label><label><input v-model="answerKind" type="radio" value="fact">Use an existing Knowledge fact</label><label><input v-model="answerKind" type="radio" value="unknown">I don’t know yet</label></fieldset><label v-if="answerKind === 'statement'">Your confirmed answer<input v-model="answerValue" maxlength="2000" required></label><p v-if="answerKind === 'statement'" class="form-note">Saving registers an attributable owner statement, creates a reviewable claim, explicitly accepts it on your behalf, and binds the resulting Fact to this answer.</p><label v-else-if="answerKind === 'fact'">Confirmed fact<select v-model="factID" required><option value="">Choose a matching fact</option><option v-for="item in matchingFacts" :key="item.id" :value="item.id">{{ item.key }} · revision {{ item.revision }}</option></select></label><div v-if="answerKind === 'fact' && matchingFacts.length === 0" class="baseline-guidance"><strong>No matching confirmed fact yet.</strong><p>Confirm an answer here or review existing proposals in Knowledge. Baseline never treats unreviewed text as authoritative.</p><RouterLink to="/app/knowledge">Open Knowledge</RouterLink></div><label v-else-if="answerKind === 'unknown'">What still needs confirmation?<textarea v-model="reason" minlength="3" maxlength="1000" rows="4" required></textarea></label><div class="baseline-actions"><IoButton v-if="question.optional" type="button" kind="secondary" @click="skipped = new Set([...skipped, question.key])">Skip optional question</IoButton><IoButton type="submit" :disabled="saving || (answerKind === 'fact' && !factID) || (answerKind === 'statement' && !answerValue.trim())">{{ saving ? "Confirming…" : "Confirm and continue" }}</IoButton></div></form></template>
        <template v-else><p class="eyebrow">Interview complete</p><h2>Ready to review your operating scope</h2><p>Spyglass will use only the confirmed facts you selected. Unknown answers remain explicit and are never filled by an agent.</p><IoButton :disabled="saving || !requiredComplete || !writable" @click="advance('inventory')">Begin inventory</IoButton></template>
      </section>
      <section v-else-if="baseline.state === 'inventory'" class="baseline-focus-card"><p class="eyebrow">Scope review</p><h2>Build the evidence inventory</h2><p>The frozen catalog version <code>{{ baseline.catalog_version }}</code> will select a proportionate set of business records from your confirmed industry, services, team size and immediate concern.</p><IoButton :disabled="saving || !writable" @click="advance('complete')">Build my evidence inventory</IoButton></section>
      <section v-else-if="baseline.state === 'gap_review'" class="baseline-focus-card"><div class="baseline-card-heading"><span>Requirement {{ reviewedCount + 1 }} of {{ baseline.requirements.length }}</span><strong>{{ reviewedCount }} reviewed</strong></div><template v-if="pendingRequirement"><p class="eyebrow">{{ label(pendingRequirement.responsibility.kind) }} responsibility</p><h2>{{ pendingRequirement.title }}</h2><p>Choose evidence that satisfies this requirement, record a real gap, or explain why it does not apply.</p><form class="baseline-form" @submit.prevent="review"><input v-model="requirementID" type="hidden"><fieldset><legend>Decision</legend><label><input v-model="disposition" type="radio" value="accepted">Accept existing Knowledge evidence</label><label><input v-model="disposition" type="radio" value="rejected">Reject reviewed evidence</label><label><input v-model="disposition" type="radio" value="gap">Create plan Work for this gap</label><label><input v-model="disposition" type="radio" value="not_applicable">Not applicable</label></fieldset><label v-if="['accepted','rejected'].includes(disposition)">Knowledge evidence ID<input v-model="evidenceID" pattern="[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}" required></label><label>Reason<textarea v-model="reviewReason" minlength="3" maxlength="1000" rows="4" required></textarea></label><p class="form-note">Accepted evidence must be user-provided or a reviewed source. Agent derivations and raw integration records cannot satisfy a Baseline requirement.</p><IoButton type="submit" :disabled="saving || !writable">{{ saving ? "Saving…" : "Record decision" }}</IoButton></form></template><template v-else><p class="eyebrow">Review complete</p><h2>Create the exact plan</h2><p>{{ gaps.length }} gaps will become accountable Work. Satisfied and not-applicable requirements remain evidence decisions.</p><IoButton :disabled="saving || !writable" @click="advance('submit')">Review frozen plan</IoButton></template></section>
      <section v-else-if="baseline.state === 'plan_approval'" class="baseline-focus-card"><p class="eyebrow">Owner approval</p><h2>{{ baseline.plan?.proposed_work_count ?? 0 }} proposed Work items</h2><ol class="baseline-requirements"><li v-for="item in gaps" :key="item.id"><strong>{{ item.title }}</strong><span>{{ item.reason }}</span></li></ol><dl class="baseline-plan-proof"><div><dt>Assessment version</dt><dd>{{ baseline.plan?.assessment_version }}</dd></div><div><dt>Content SHA-256</dt><dd>{{ baseline.plan?.content_sha256 }}</dd></div></dl><p class="form-note">Approval binds this exact version and digest. Any changed evidence requires a new plan.</p><IoButton v-if="manageable" :disabled="saving" @click="advance('approve')">Approve exact plan</IoButton><p v-else class="queue-inline-status">An Owner or Administrator must approve this plan.</p></section>
      <section v-else class="baseline-dashboard">
        <header><div><p class="eyebrow">{{ baseline.state === 'ready' ? 'Operating Baseline' : 'Approved plan' }}</p><h2>{{ baseline.state === 'ready' ? 'Your Baseline is active' : 'Put the plan into motion' }}</h2><p>{{ baseline.state === 'ready' ? `Next reassessment ${date(baseline.reassess_at)}.` : 'Create accountable Work for each approved gap, then confirm completed evidence.' }}</p></div><span class="state-badge">{{ label(baseline.state) }}</span></header><section class="baseline-stat-grid"><article><strong>{{ baseline.requirements.length }}</strong><span>requirements</span></article><article><strong>{{ baseline.requirements.filter((item) => item.disposition === 'satisfied').length }}</strong><span>satisfied</span></article><article><strong>{{ gaps.length }}</strong><span>open gaps</span></article></section>
        <section v-if="baseline.state === 'active'" class="baseline-panel"><h3>Approved plan Work</h3><p v-if="planMaterialized">The approved gap plan is represented in Work.</p><IoButton v-else :disabled="saving || !manageable" @click="advance('materialize')">Create {{ gaps.length }} Work items</IoButton><ol class="baseline-requirements"><li v-for="item in linkedWork" :key="item.id"><RouterLink :to="`/app/work/${item.id}`">#{{ item.number }} · {{ item.title }}</RouterLink><span>{{ label(item.state) }}</span></li></ol></section>
        <section v-if="gaps.length" class="baseline-panel"><h3>Confirm completed Work evidence</h3><p>A completed task is not evidence by itself. Bind the exact completed Work and reviewed Knowledge evidence.</p><form class="baseline-form" @submit.prevent="confirmWork"><label>Completed Baseline Work<select v-model="workItemID" required><option value="">Choose completed Work</option><option v-for="item in linkedWork.filter((entry) => entry.state === 'done')" :key="item.id" :value="item.id">#{{ item.number }} · {{ item.title }}</option></select></label><label>Knowledge evidence ID<input v-model="evidenceID" pattern="[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}" required></label><label>Review reason<textarea v-model="reviewReason" minlength="3" maxlength="1000" rows="3" required></textarea></label><IoButton type="submit" :disabled="saving || !writable">Confirm evidence</IoButton></form></section>
        <section class="baseline-panel"><h3>Read-only source access</h3><p>Grant only an active Email or Google Drive connection whose scope has already been reviewed in Integrations.</p><form v-if="sourcesManageable" class="baseline-form" @submit.prevent="grantSource"><label>Connection<select v-model="connectionID" required @change="selectConnection"><option value="">Choose active connection</option><option v-for="item in sourceConnections" :key="item.id" :value="item.id">{{ item.name }} · {{ label(item.kind) }}</option></select></label><label v-if="selectedConnection">Folders, one per line<textarea v-model="folders" rows="3" required></textarea></label><div class="baseline-date-grid"><label>Read since<input v-model="sinceAt" type="datetime-local"></label><label>Read until<input v-model="untilAt" type="datetime-local"></label></div><IoButton type="submit" :disabled="saving || !selectedConnection">Grant narrow read access</IoButton></form><p v-else class="queue-inline-status">Manage sources with an enabled Integrations package as Owner or Administrator.</p><ol class="baseline-requirements"><li v-for="grant in grants" :key="grant.id"><div><strong>{{ label(grant.source_kind) }} · {{ label(grant.state) }}</strong><span>{{ grant.folders.join(', ') }}</span></div><form v-if="grant.state === 'active' && sourcesManageable" class="baseline-revoke" @submit.prevent="revoke(grant)"><input v-model="revokeReason" minlength="3" maxlength="1000" placeholder="Reason for revocation" required><IoButton type="submit" kind="secondary">Revoke</IoButton></form></li></ol></section>
        <section class="baseline-actions baseline-final-actions"><IoButton v-if="baseline.state === 'active' && manageable" :disabled="saving || !readyForReady" @click="advance('ready')">Mark Baseline ready</IoButton><IoButton kind="secondary" :disabled="saving || !manageable" @click="advance('maintenance')">Create due maintenance Work</IoButton><IoButton v-if="baseline.state === 'ready' && manageable" kind="secondary" :disabled="saving" @click="advance('reassess')">Start reassessment</IoButton></section><p v-if="baseline.state === 'active' && !readyForReady" class="queue-inline-status">Resolve every remaining gap with reviewed evidence before marking the Baseline ready.</p>
      </section>
    </template>
  </section>
</template>
