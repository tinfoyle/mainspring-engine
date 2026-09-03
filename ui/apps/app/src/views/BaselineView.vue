<script setup lang="ts">
import {
  answerBaseline, approveBaselinePlan, beginBaselineInventory, captureOwnerKnowledgeEvidence, captureOwnerKnowledgeFact, completeBaselineInventory,
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
  { key: "organization.legal_name", prompt: "What is the legal or registered name of your business?", explanation: "I’ll use this to make sure the setup belongs to the right business." },
  { key: "organization.website_url", prompt: "Does your business have a website?", explanation: "If it does, the website gives me a safe place to learn the basics.", optional: true },
  { key: "organization.industry", prompt: "What kind of work does your business do?", explanation: "This helps me avoid giving you a checklist meant for a completely different business." },
  { key: "organization.primary_location", prompt: "Where do you mainly do business?", explanation: "A city and state is enough. Location can change which licenses, insurance, and records matter." },
  { key: "organization.services", prompt: "What do customers pay you to do?", explanation: "A short, everyday description is perfect." },
  { key: "organization.team_size", prompt: "How many people regularly work in the business?", explanation: "Include owners, employees, and regular contractors." },
  { key: "baseline.immediate_concern", prompt: "What is the biggest thing you want help getting under control?", explanation: "I’ll keep the first plan focused on what is bothering you now." }
];
const stages = ["interview", "inventory", "gap_review", "plan_approval", "active", "ready"] as const;
const stageNames: Readonly<Record<typeof stages[number], string>> = {
  interview: "Your business", inventory: "What matters", gap_review: "What you have", plan_approval: "Your plan", active: "Get it done", ready: "Ready"
};
const requirementQuestions: Readonly<Record<string, string>> = {
  identity_registration: "Do you have your business registration and ownership paperwork?",
  licenses_permits: "Do you keep a current list or copies of the licenses and permits you need?",
  insurance: "Do you have your current business insurance documents handy?",
  compliance_calendar: "Do you track filing, license, and insurance renewal dates in one place?",
  financial_reporting: "Can you quickly find recent reports showing income, expenses, and cash?",
  billing_collection: "Do you have a consistent way to invoice customers and follow up on overdue bills?",
  sales_pipeline: "Do you keep track of leads, quotes, won jobs, and lost jobs?",
  service_workflow: "Does your team have a repeatable way to take a job from request to completion?",
  quality_closeout: "Do you use a checklist before calling a job finished?",
  customer_terms: "Do customers receive clear terms about the work, price, and what is not included?",
  customer_feedback: "Do you have one place to track customer problems, complaints, and feedback?",
  role_responsibility: "Is it clear who owns the important jobs and decisions in the business?",
  workforce_records: "Do you keep the employee and contractor records your business needs?",
  goals_scorecard: "Do you regularly track a few business goals or important numbers?",
  product_definition: "Is it written down what you sell, who it is for, and how you price it?",
  software_delivery: "Do you have a repeatable way to test, release, and roll back software changes?",
  security_access_controls: "Do you keep track of important systems, access, and who is responsible for security?",
  privacy_data_handling: "Do you have a clear record of what customer data you keep and why?",
  incident_continuity: "Do you have a plan for outages, lost data, or other serious disruptions?",
  fulfillment_inventory: "Do you have a repeatable way to manage stock, delivery, returns, and refunds?"
};
const session = useSessionStore(); const route = useRoute(); const router = useRouter();
const baseline = ref<BaselineAssessment>(); const facts = ref<ReadonlyArray<KnowledgeFactSummary>>([]); const work = ref<ReadonlyArray<WorkItem>>([]);
const connections = ref<ReadonlyArray<IntegrationConnection>>([]); const selectedConnection = ref<IntegrationConnectionDetail>(); const grants = ref<ReadonlyArray<BaselineSourceGrant>>([]);
const loading = ref(false); const saving = ref(false); const error = ref(""); const announcement = ref(""); const navigationNotice = ref(""); let sequence = 0;
const skipped = ref(new Set<string>()); const answerKind = ref<"fact" | "statement" | "unknown">("statement"); const factID = ref(""); const answerValue = ref(""); const reason = ref("");
const requirementID = ref(""); const evidenceID = ref(""); const disposition = ref<"" | "accepted" | "gap" | "not_applicable">(""); const reviewReason = ref("");
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
const interviewPosition = computed(() => Math.min(questions.length, answeredKeys.value.size + skipped.value.size + 1));
const matchingFacts = computed(() => facts.value.filter((item) => item.key === question.value?.key && item.state === "active"));
const requiredComplete = computed(() => questions.filter((item) => !("optional" in item)).every((item) => answeredKeys.value.has(item.key)));
const pendingRequirement = computed(() => baseline.value?.requirements.find((item) => item.disposition === "pending"));
const gaps = computed(() => baseline.value?.requirements.filter((item) => item.disposition === "gap") ?? []);
const reviewedCount = computed(() => baseline.value?.requirements.filter((item) => item.disposition !== "pending").length ?? 0);
const linkedWork = computed(() => work.value.filter((item) => item.provenance.source === "baseline" && gaps.value.some((gap) => gap.id === item.provenance.baseline_requirement_id)));
const planMaterialized = computed(() => gaps.value.every((gap) => linkedWork.value.some((item) => item.provenance.baseline_requirement_id === gap.id)));
const readyForReady = computed(() => baseline.value?.requirements.every((item) => ["satisfied", "not_applicable"].includes(item.disposition)) ?? false);
const sourceConnections = computed(() => connections.value.filter((item) => item.state === "active" && ["email", "google_drive"].includes(item.kind)));
const reviewReady = computed(() => Boolean(disposition.value) && (!["accepted", "not_applicable"].includes(disposition.value) || reviewReason.value.trim().length >= 3));
const hasUnsavedBaselineWork = computed(() => {
  if (!baseline.value) return false;
  if (baseline.value.state === "interview") {
    return Boolean(skipped.value.size || answerKind.value !== "statement" || factID.value || answerValue.value.trim() || reason.value.trim());
  }
  if (baseline.value.state === "gap_review") {
    return Boolean(disposition.value || evidenceID.value || reviewReason.value.trim());
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
function stageLabel(value: typeof stages[number]): string { return stageNames[value]; }
function requirementQuestion(value: BaselineRequirement): string { return requirementQuestions[value.code] ?? `Do you already have ${value.title.toLocaleLowerCase()} in place?`; }
function responsibilityLine(value: BaselineRequirement): string {
  if (value.responsibility.kind === "persona") return "Spyglass can help keep this up to date.";
  if (value.responsibility.kind === "user") return "This needs a person in your business to own it.";
  return "You and Spyglass may share the follow-up on this.";
}
function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value)) : "Not set"; }
function planInput(value: BaselineAssessment) { if (!value.plan) throw new Error("The frozen plan is unavailable."); return { plan_id: value.plan.id, content_sha256: value.plan.content_sha256, assessment_version: value.plan.assessment_version }; }
function resetReview(value?: BaselineRequirement): void { requirementID.value = value?.id ?? ""; evidenceID.value = ""; disposition.value = ""; reviewReason.value = ""; }
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
  try {
    if (disposition.value === "accepted") {
      if (!evidenceID.value) {
        const evidence = await captureOwnerKnowledgeEvidence(accountID, reviewReason.value, `baseline/${value.id}/requirements/${pendingRequirement.value?.code ?? requirementID.value}/owner-confirmation`);
        evidenceID.value = evidence.id;
      }
      baseline.value = await decideBaselineEvidence(accountID, value, { requirement_id: requirementID.value, evidence_id: evidenceID.value, decision: "accepted", reason: reviewReason.value });
    } else {
      const dispositionReason = reviewReason.value.trim() || "We do not have this in place yet. Add it to the Business Baseline plan.";
      baseline.value = await dispositionBaselineRequirement(accountID, value, { requirement_id: requirementID.value, disposition: disposition.value as "gap" | "not_applicable", reason: dispositionReason });
    }
    resetReview(pendingRequirement.value); announcement.value = "Thanks. I saved that answer.";
  }
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
    <header class="page-heading"><p class="eyebrow">Set up Spyglass</p><h1>Business Baseline</h1><p>I’ll learn how your business works, show you what may be missing, and help turn it into a practical plan. You can stop and come back at any time.</p></header>
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <div v-if="loading" class="queue-state"><h2>Loading your Baseline…</h2></div>
    <div v-else-if="!available" class="queue-state queue-state--warning"><h2>Knowledge is not available</h2><p>Your selected Account does not currently include readable Knowledge access.</p></div>
    <div v-else-if="error && !baseline" class="queue-state queue-state--error"><h2>Baseline could not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></div>
    <section v-else-if="!baseline" class="baseline-welcome">
      <div class="baseline-agent-row">
        <span class="baseline-agent-avatar" aria-hidden="true">S</span>
        <div class="baseline-agent-bubble">
          <p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p>
          <h2>Let’s get your business set up.</h2>
          <p>I’ll ask a few straightforward questions, then we’ll look at what you already have and what needs attention. If you do not know an answer, just say so—we can put it on the plan.</p>
          <small>Most people finish this first conversation in about 10–15 minutes.</small>
        </div>
      </div>
      <IoButton v-if="manageable" :disabled="saving" @click="start">{{ saving ? "Starting…" : "Let’s get started" }}</IoButton>
      <p v-else class="queue-inline-status">An Owner or Administrator must start the first Baseline.</p>
    </section>
    <template v-else>
      <nav class="baseline-progress" aria-label="Baseline progress" tabindex="0"><ol><li v-for="(stage, index) in stages" :key="stage" :class="{ done: index < currentStage, current: index === currentStage }"><span>{{ index + 1 }}</span><small>{{ stageLabel(stage) }}</small></li></ol></nav>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section v-if="baseline.state === 'interview'" class="baseline-focus-card">
        <template v-if="question">
          <div class="baseline-card-heading"><span>Question {{ interviewPosition }} of {{ questions.length }}</span><strong>{{ Math.round(((interviewPosition - 1) / questions.length) * 100) }}% complete</strong></div>
          <div class="baseline-agent-row">
            <span class="baseline-agent-avatar" aria-hidden="true">S</span>
            <div class="baseline-agent-bubble">
              <p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p>
              <h2>{{ question.prompt }}</h2>
              <p>{{ question.explanation }}</p>
            </div>
          </div>
          <form class="baseline-form baseline-reply" @submit.prevent="saveAnswer">
            <fieldset class="baseline-reply-choices">
              <legend>Choose how you want to reply</legend>
              <label><input v-model="answerKind" type="radio" value="statement"><span><strong>I can answer this</strong><small>Tell me in your own words.</small></span></label>
              <label><input v-model="answerKind" type="radio" value="unknown"><span><strong>I’m not sure yet</strong><small>We’ll keep it honest and come back to it.</small></span></label>
              <label v-if="matchingFacts.length"><input v-model="answerKind" type="radio" value="fact"><span><strong>Use a saved answer</strong><small>Choose something you already confirmed in Spyglass.</small></span></label>
            </fieldset>
            <label v-if="answerKind === 'statement'">Your answer<textarea v-model="answerValue" maxlength="2000" rows="3" required></textarea></label>
            <p v-if="answerKind === 'statement'" class="form-note">I’ll save this as your confirmed answer so Spyglass can use it later.</p>
            <label v-else-if="answerKind === 'fact'">Saved answer<select v-model="factID" required><option value="">Choose a saved answer</option><option v-for="item in matchingFacts" :key="item.id" :value="item.id">Confirmed {{ date(item.updated_at) }}</option></select></label>
            <label v-else>What should I help you confirm?<textarea v-model="reason" minlength="3" maxlength="1000" rows="3" required></textarea></label>
            <div class="baseline-actions"><IoButton v-if="question.optional" type="button" kind="secondary" @click="skipped = new Set([...skipped, question.key])">Skip this question</IoButton><IoButton type="submit" :disabled="saving || (answerKind === 'fact' && !factID) || (answerKind === 'statement' && !answerValue.trim())">{{ saving ? "Saving…" : "Continue" }}</IoButton></div>
          </form>
        </template>
        <template v-else>
          <div class="baseline-agent-row"><span class="baseline-agent-avatar" aria-hidden="true">S</span><div class="baseline-agent-bubble"><p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p><h2>Thanks. I have enough to make your checklist.</h2><p>I’ll use only the answers you confirmed. Anything you were unsure about will stay marked as unknown.</p></div></div>
          <IoButton :disabled="saving || !requiredComplete || !writable" @click="advance('inventory')">Build my checklist</IoButton>
        </template>
      </section>
      <section v-else-if="baseline.state === 'inventory'" class="baseline-focus-card">
        <div class="baseline-agent-row"><span class="baseline-agent-avatar" aria-hidden="true">S</span><div class="baseline-agent-bubble"><p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p><h2>I’m ready to build a checklist for your business.</h2><p>It will cover the records and routines that are likely to matter based on what you told me. You’ll review every item before anything is added to your plan.</p></div></div>
        <IoButton :disabled="saving || !writable" @click="advance('complete')">Show me the checklist</IoButton>
      </section>
      <section v-else-if="baseline.state === 'gap_review'" class="baseline-focus-card">
        <div class="baseline-card-heading"><span>Check {{ reviewedCount + 1 }} of {{ baseline.requirements.length }}</span><strong>{{ reviewedCount }} answered</strong></div>
        <template v-if="pendingRequirement">
          <div class="baseline-agent-row">
            <span class="baseline-agent-avatar" aria-hidden="true">S</span>
            <div class="baseline-agent-bubble">
              <p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p>
              <h2>{{ requirementQuestion(pendingRequirement) }}</h2>
              <p>{{ responsibilityLine(pendingRequirement) }} I’m checking whether this is already handled or should go on your plan.</p>
            </div>
          </div>
          <form class="baseline-form baseline-reply" @submit.prevent="review">
            <input v-model="requirementID" type="hidden">
            <fieldset class="baseline-reply-choices">
              <legend>Choose your answer</legend>
              <label><input v-model="disposition" type="radio" value="accepted"><span><strong>Yes, we have a way</strong><small>Tell me what you use or where you keep it.</small></span></label>
              <label><input v-model="disposition" type="radio" value="gap"><span><strong>Not yet—add it to my plan</strong><small>Spyglass will turn this into a clear task.</small></span></label>
              <label><input v-model="disposition" type="radio" value="not_applicable"><span><strong>This doesn’t apply to us</strong><small>Tell me why so I do not keep asking.</small></span></label>
            </fieldset>
            <label v-if="disposition === 'accepted'">What do you use, and where is it kept?<textarea v-model="reviewReason" minlength="3" maxlength="1000" rows="3" placeholder="For example: We log customer complaints in Jobber and review them every Friday." required></textarea></label>
            <label v-else-if="disposition === 'gap'">Anything I should know before adding this to your plan? <span class="field-optional">Optional</span><textarea v-model="reviewReason" maxlength="1000" rows="3" placeholder="Add any useful details, or leave this blank."></textarea></label>
            <label v-else-if="disposition === 'not_applicable'">Why doesn’t this apply to your business?<textarea v-model="reviewReason" minlength="3" maxlength="1000" rows="3" required></textarea></label>
            <p class="form-note">Your reply is saved with your name and the time. Spyglass will not quietly change your answer later.</p>
            <IoButton type="submit" :disabled="saving || !writable || !reviewReady">{{ saving ? "Saving…" : "Save and keep going" }}</IoButton>
          </form>
        </template>
        <template v-else>
          <div class="baseline-agent-row"><span class="baseline-agent-avatar" aria-hidden="true">S</span><div class="baseline-agent-bubble"><p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p><h2>That’s the checklist finished.</h2><p>I found {{ gaps.length }} {{ gaps.length === 1 ? 'item' : 'items' }} to put on your plan. You’ll see the full list before approving anything.</p></div></div>
          <IoButton :disabled="saving || !writable" @click="advance('submit')">Show me my plan</IoButton>
        </template>
      </section>
      <section v-else-if="baseline.state === 'plan_approval'" class="baseline-focus-card">
        <div class="baseline-agent-row"><span class="baseline-agent-avatar" aria-hidden="true">S</span><div class="baseline-agent-bubble"><p class="baseline-agent-name"><strong>Spyglass</strong><span>Setup guide</span></p><h2>Here’s the plan I made from our conversation.</h2><p>Nothing starts until you approve it. You can review each item below first.</p></div></div>
        <ol class="baseline-requirements"><li v-for="item in gaps" :key="item.id"><strong>{{ item.title }}</strong><span>{{ item.reason }}</span></li></ol>
        <details class="baseline-technical"><summary>Technical record for this plan</summary><dl class="baseline-plan-proof"><div><dt>Assessment version</dt><dd>{{ baseline.plan?.assessment_version }}</dd></div><div><dt>Content SHA-256</dt><dd>{{ baseline.plan?.content_sha256 }}</dd></div></dl><p>This record makes sure the plan you approve is the same plan Spyglass carries out.</p></details>
        <IoButton v-if="manageable" :disabled="saving" @click="advance('approve')">Looks good—approve this plan</IoButton><p v-else class="queue-inline-status">An Owner or Administrator must approve this plan.</p>
      </section>
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
