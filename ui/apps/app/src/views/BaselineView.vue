<script setup lang="ts">
import {
  APIProblem, assignWork, captureOwnerKnowledgeFact, configureAgentManager, createAgentBoardroom, createWorkFromAgentMessage,
  getAgentRun, getBaseline, getCurrentBaseline, getWorkItem, isAPIProblem, linkWorkConversation, listAgentBoardrooms,
  listAgentConversations, listAgentMessages, listAgentPersonas, listKnowledgeFacts, listWork, publishAgentPersona,
  resolveAgentRun, startAgentRun, startBaseline, type AgentBaselineAutomationOffer, type AgentBaselineInterview, type AgentBoardroom,
  type AgentMessage, type AgentPersona, type AgentRun, type BaselineAssessment, type KnowledgeFactSummary, type WorkItem
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  baselinePersonaDescription, baselinePersonaInstructions, baselinePersonaName, baselinePersonaPolicy,
  baselinePersonaRole, baselineRoomName, baselineRoomPurpose, validBaselineQuestionKey
} from "../baseline-interviewer";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const bootstrapPrompt = "Please begin my Business Baseline interview. Introduce yourself briefly, explain that you will learn how my business works and save my answers for later, then ask the single best first question.";
const session = useSessionStore(); const route = useRoute(); const router = useRouter();
const baseline = ref<BaselineAssessment>(); const room = ref<AgentBoardroom>(); const persona = ref<AgentPersona>();
const messages = ref<ReadonlyArray<AgentMessage>>([]); const pendingMessages = ref<ReadonlyArray<AgentMessage>>([]); const facts = ref<ReadonlyArray<KnowledgeFactSummary>>([]); const work = ref<ReadonlyArray<WorkItem>>([]); const activeRun = ref<AgentRun>();
const reply = ref(""); const loading = ref(false); const preparing = ref(false); const sending = ref(false); const recovering = ref(false); const error = ref(""); const announcement = ref(""); const navigationNotice = ref(""); const chat = ref<HTMLElement>();
let loadSequence = 0; let pollTimer: number | undefined; let pollGeneration = 0;
let suppressedAssessmentRoute = "";
const maximumAutomaticRetries = 2;

const knowledgePackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "knowledge"));
const agentsPackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "agents"));
const workPackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "work"));
const available = computed(() => Boolean(knowledgePackage.value && knowledgePackage.value.mode !== "suspended" && agentsPackage.value && agentsPackage.value.mode !== "suspended"));
const manageable = computed(() => Boolean(session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator"].includes(session.selected.role) && knowledgePackage.value?.mode === "enabled" && agentsPackage.value?.mode === "enabled"));
const conversationID = computed(() => messages.value[0]?.conversation_id ?? activeRun.value?.conversation_id ?? "");
const visibleMessages = computed(() => {
  const persistedIDs = new Set(messages.value.map((item) => item.id));
  return [
    ...messages.value.filter((item) => item.body !== bootstrapPrompt),
    ...pendingMessages.value.filter((item) => !persistedIDs.has(item.id))
  ];
});
const latestAgentMessage = computed(() => [...messages.value].reverse().find((item) => item.role === "persona" && item.result?.baseline));
function listOrEmpty<T>(value: ReadonlyArray<T> | null | undefined): ReadonlyArray<T> { return Array.isArray(value) ? value : []; }
const interview = computed<AgentBaselineInterview | undefined>(() => {
  const value = latestAgentMessage.value?.result?.baseline; if (!value) return undefined;
  return {
    ...value,
    captured_topics: listOrEmpty(value.captured_topics),
    automation_offers: listOrEmpty(value.automation_offers),
    approved_work: listOrEmpty(value.approved_work),
    missing_topics: listOrEmpty(value.missing_topics)
  };
});
const ready = computed(() => Boolean(interview.value?.ready));
const capturedTopicCount = computed(() => new Set(facts.value.filter((item) => item.key.startsWith("baseline.")).map((item) => item.key)).size);
const baselineWork = computed(() => work.value.filter((item) => item.provenance.conversation_id === conversationID.value));
const runNeedsAttention = computed(() => Boolean(activeRun.value && ["failed", "partially_failed"].includes(activeRun.value.state)));
const runPending = computed(() => recovering.value || Boolean(activeRun.value && !["succeeded", "partially_failed", "failed", "canceled"].includes(activeRun.value.state)));
const invalidModelOutput = computed(() => Boolean(activeRun.value?.invocations.some((item) => item.status === "failed" && item.failure_code === "model_output_invalid")));
const hasDraft = computed(() => Boolean(reply.value.trim()));
function approvalTitle(body: string): string {
  return body.match(/^Yes, add [“"](.+)[”"] to the work we will set up\.$/)?.[1] ?? "";
}
const approvedOfferTitles = computed(() => new Set([...messages.value, ...pendingMessages.value].filter((item) => item.role === "user").map((item) => approvalTitle(item.body)).filter(Boolean)));
const availableOffers = computed(() => listOrEmpty(interview.value?.automation_offers).filter((offer) => !approvedOfferTitles.value.has(offer.title)));

const { allowNextNavigation } = useSafeNavigation({ dirty: hasDraft, pending: sending, message: "Leave Business Baseline? Your unsent reply will remain only in this browser tab.", onBlocked: (reason) => { navigationNotice.value = reason === "pending" ? "Your operations agent is still working. Stay here until the reply arrives." : "Navigation canceled. Your reply is still here."; } });

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function messageName(item: AgentMessage): string { return item.role === "user" ? "You" : persona.value?.name ?? "Operations Guide"; }
function messageBody(item: AgentMessage): string {
  const approved = item.role === "user" ? approvalTitle(item.body) : "";
  if (approved) return `Approved setup work: ${approved}`;
  const question = item.role === "persona" ? item.result?.baseline?.next_question?.trim() ?? "" : "";
  return question && !item.body.includes(question) ? `${item.body}\n\n${question}` : item.body;
}
function messageClass(item: AgentMessage): string[] { return ["baseline-message", `baseline-message--${item.role}`, ...(approvalTitle(item.body) ? ["baseline-message--approval"] : [])]; }
function messageTime(value: string): string { return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(value)); }
function handle(operation: string, cause: unknown): void { error.value = cause instanceof APIProblem || cause instanceof Error ? cause.message : `Could not ${operation}.`; }
function runIsTerminal(run: AgentRun): boolean { return ["succeeded", "partially_failed", "failed", "canceled"].includes(run.state); }
async function scrollToLatest(): Promise<void> { await nextTick(); chat.value?.scrollTo({ top: chat.value.scrollHeight, behavior: "smooth" }); }

async function synchronizeAssessmentRoute(assessmentID: string): Promise<void> {
  if (route.params.assessmentID === assessmentID) return;
  suppressedAssessmentRoute = assessmentID; allowNextNavigation();
  await router.replace(`/app/baseline/${assessmentID}`);
}

async function loadSupporting(accountID: string): Promise<void> {
  const [knowledge, workItems] = await Promise.allSettled([listKnowledgeFacts(accountID), listWork(accountID, { limit: 100 })]);
  facts.value = knowledge.status === "fulfilled" ? knowledge.value : []; work.value = workItems.status === "fulfilled" ? workItems.value.items : [];
}

async function ensureOperationsGuide(accountID: string): Promise<void> {
  preparing.value = true;
  try {
    const rooms = await listAgentBoardrooms(accountID); room.value = rooms.find((item) => item.purpose === baselineRoomPurpose);
    if (!room.value) room.value = await createAgentBoardroom(accountID, { name: baselineRoomName, purpose: baselineRoomPurpose });
    const personas = await listAgentPersonas(accountID, room.value.id); persona.value = personas.find((item) => item.description === baselinePersonaDescription);
    if (!persona.value || persona.value.system_instructions !== baselinePersonaInstructions) {
      persona.value = await publishAgentPersona(accountID, room.value.id, {
        persona_id: persona.value?.id ?? crypto.randomUUID(), expected_latest_version: persona.value?.latest_version ?? 0,
        name: baselinePersonaName, role: baselinePersonaRole, description: baselinePersonaDescription,
        system_instructions: baselinePersonaInstructions, policy: baselinePersonaPolicy
      });
    }
    if (room.value.manager_persona_id !== persona.value.id) room.value = await configureAgentManager(accountID, room.value.id, { manager_persona_id: persona.value.id, expected_version: room.value.version });
    const conversations = await listAgentConversations(accountID, room.value.id);
    const existing = conversations.find((item) => item.subject === `Business Baseline · ${baseline.value?.id ?? ""}`);
    if (existing) {
      messages.value = await listAgentMessages(accountID, existing.id);
      const latestRunID = [...messages.value].reverse().find((item) => item.role === "user" && item.run_id)?.run_id
        ?? [...messages.value].reverse().find((item) => item.run_id)?.run_id;
      if (latestRunID) {
        const resumed = await followRetryChain(accountID, latestRunID);
        activeRun.value = resumed.run;
        if (!runIsTerminal(resumed.run) || ["failed", "partially_failed"].includes(resumed.run.state)) beginPolling(resumed.run, resumed.retryCount);
      }
      await materializeApprovedWork(accountID);
    }
  } finally { preparing.value = false; }
}

async function load(): Promise<void> {
  const accountID = session.selectedID; const sequence = ++loadSequence;
  baseline.value = undefined; room.value = undefined; persona.value = undefined; messages.value = []; pendingMessages.value = []; activeRun.value = undefined; error.value = "";
  if (!accountID || !available.value) return; loading.value = true;
  try {
    const id = typeof route.params.assessmentID === "string" ? route.params.assessmentID : "";
    try { baseline.value = id ? await getBaseline(accountID, id) : await getCurrentBaseline(accountID); }
    catch (cause) { if (!isAPIProblem(cause) || cause.status !== 404 || id) throw cause; }
    if (sequence !== loadSequence) return;
    if (baseline.value && !id) await synchronizeAssessmentRoute(baseline.value.id);
    await loadSupporting(accountID); if (baseline.value && manageable.value) await ensureOperationsGuide(accountID); await scrollToLatest();
  } catch (cause) { handle("load Business Baseline", cause); }
  finally { if (sequence === loadSequence) loading.value = false; }
}

async function begin(): Promise<void> {
  const accountID = session.selectedID; if (!accountID || !manageable.value || sending.value) return; sending.value = true; error.value = "";
  try {
    if (!baseline.value) { baseline.value = await startBaseline(accountID); await synchronizeAssessmentRoute(baseline.value.id); }
    await ensureOperationsGuide(accountID);
    if (messages.value.length === 0) {
      const run = await runInterview(accountID, bootstrapPrompt, false); if (run) beginPolling(run, 0);
    }
  } catch (cause) { handle("start your interview", cause); }
  finally { sending.value = false; }
}

async function send(value: string, captureKnowledge = true): Promise<void> {
  const accountID = session.selectedID; value = value.trim(); const questionKey = interview.value?.next_question_key ?? "";
  if (!accountID || !value || !room.value || !persona.value || sending.value || recovering.value || runNeedsAttention.value || ready.value) return;
  const draft = reply.value; const temporaryID = crypto.randomUUID();
  pendingMessages.value = [...pendingMessages.value, {
    id: temporaryID, conversation_id: conversationID.value, sequence: messages.value.length + pendingMessages.value.length + 1,
    role: "user", body: value, ...(session.userID ? { created_by: session.userID } : {}), created_at: new Date().toISOString()
  }];
  if (captureKnowledge) reply.value = "";
  sending.value = true; error.value = ""; navigationNotice.value = "";
  await scrollToLatest();
  try {
    if (captureKnowledge && validBaselineQuestionKey(questionKey) && baseline.value) {
      const fact = await captureOwnerKnowledgeFact(accountID, questionKey, value, `baseline/${baseline.value.id}/conversation/${conversationID.value || "new"}/${questionKey}`);
      facts.value = [...facts.value.filter((item) => item.id !== fact.id), fact];
    }
    const run = await runInterview(accountID, value, true);
    if (run) {
      pendingMessages.value = pendingMessages.value.map((item) => item.id === temporaryID ? { ...item, id: run.user_message_id, conversation_id: run.conversation_id } : item);
      beginPolling(run, 0);
    }
  } catch (cause) {
    pendingMessages.value = pendingMessages.value.filter((item) => item.id !== temporaryID);
    if (captureKnowledge) reply.value = draft || value;
    handle("send your answer", cause);
  }
  finally { sending.value = false; }
}

async function acceptOffer(offer: AgentBaselineAutomationOffer): Promise<void> { await send(`Yes, add “${offer.title}” to the work we will set up.`, false); }

async function submitReply(): Promise<void> {
  if (sending.value || runPending.value || !reply.value.trim()) return;
  await send(reply.value, true);
}

function handleReplyKeydown(event: KeyboardEvent): void {
  if (event.key !== "Enter" || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  void submitReply();
}

async function runInterview(accountID: string, prompt: string, continuing: boolean): Promise<AgentRun | undefined> {
  if (!room.value || !persona.value || !baseline.value) return undefined;
  const input = {
    prompt, mode: "selected" as const, persona_ids: [persona.value.id],
    context: { baseline_assessment_ids: [baseline.value.id], knowledge_fact_ids: facts.value.slice(-63).map((item) => item.id) },
    ...(continuing && conversationID.value ? { conversation_id: conversationID.value } : { subject: `Business Baseline · ${baseline.value.id}` })
  };
  const run = await startAgentRun(accountID, room.value.id, input); activeRun.value = run; await scrollToLatest();
  return run;
}

function malformedOutputFailure(run: AgentRun): boolean {
  const failed = run.invocations.filter((item) => item.status === "failed");
  return failed.length > 0 && failed.every((item) => item.failure_code === "model_output_invalid");
}

async function followRetryChain(accountID: string, initialRunID: string): Promise<{ run: AgentRun; retryCount: number }> {
  let run = await getAgentRun(accountID, initialRunID); let retryCount = 0; const visited = new Set([run.id]);
  while (retryCount < 8) {
    const retryRunID = run.resolutions.find((item) => item.action === "retry_failed")?.retry_run_id;
    if (!retryRunID || visited.has(retryRunID)) break;
    visited.add(retryRunID); run = await getAgentRun(accountID, retryRunID); retryCount += 1;
  }
  return { run, retryCount };
}

async function createRetry(accountID: string, failedRun: AgentRun, note: string): Promise<AgentRun> {
  try {
    const resolution = await resolveAgentRun(accountID, failedRun.id, { action: "retry_failed", note });
    if (!resolution.retry_run_id) throw new Error("The retry did not create a replacement run.");
    return await getAgentRun(accountID, resolution.retry_run_id);
  } catch (cause) {
    if (!isAPIProblem(cause) || ![409, 412].includes(cause.status)) throw cause;
    const resumed = await followRetryChain(accountID, failedRun.id);
    if (resumed.run.id === failedRun.id) throw cause;
    return resumed.run;
  }
}

async function retryFailedRun(): Promise<void> {
  const accountID = session.selectedID; const failedRun = activeRun.value;
  if (!accountID || !failedRun || !runNeedsAttention.value || recovering.value) return;
  recovering.value = true; error.value = ""; navigationNotice.value = "";
  try {
    const retry = await createRetry(accountID, failedRun, "Retry the Business Baseline reply without duplicating the owner's answer.");
    activeRun.value = retry; announcement.value = "Your operations agent is trying that reply again.";
    beginPolling(retry, maximumAutomaticRetries);
  } catch (cause) { handle("retry the interview reply", cause); }
  finally { recovering.value = false; }
}

function beginPolling(run: AgentRun, automaticRetryCount: number): void {
  if (pollTimer !== undefined) window.clearTimeout(pollTimer); const generation = ++pollGeneration; let attempt = 0;
  const poll = async (): Promise<void> => {
    const accountID = session.selectedID; if (!accountID || generation !== pollGeneration) return;
    try {
      const current = attempt === 0 ? run : await getAgentRun(accountID, run.id); if (generation !== pollGeneration) return; activeRun.value = current;
      if (runIsTerminal(current) || attempt >= 79) {
        messages.value = await listAgentMessages(accountID, current.conversation_id);
        const persistedIDs = new Set(messages.value.map((item) => item.id)); pendingMessages.value = pendingMessages.value.filter((item) => !persistedIDs.has(item.id));
        await materializeApprovedWork(accountID);
        if (["failed", "partially_failed"].includes(current.state) && malformedOutputFailure(current) && automaticRetryCount < maximumAutomaticRetries) {
          recovering.value = true; announcement.value = "The first reply did not pass validation. Your operations agent is trying again.";
          try {
            const retry = await createRetry(accountID, current, "Automatically retry malformed Business Baseline output.");
            if (generation !== pollGeneration) return;
            activeRun.value = retry; recovering.value = false; beginPolling(retry, automaticRetryCount + 1); return;
          } catch (cause) { recovering.value = false; handle("retry the interview reply", cause); }
        }
        announcement.value = current.state === "succeeded" ? "Your operations agent replied." : "The interview run needs attention."; await scrollToLatest(); return;
      }
      attempt += 1; pollTimer = window.setTimeout(() => void poll(), 1500);
    } catch (cause) { handle("refresh the interview", cause); }
  };
  void poll();
}

async function materializeApprovedWork(accountID: string): Promise<void> {
  if (!persona.value || workPackage.value?.mode !== "enabled") return;
  const approvals = messages.value.flatMap((message) => message.role === "persona"
    ? listOrEmpty(message.result?.baseline?.approved_work).map((proposal) => ({ message, proposal }))
    : []);
  for (const { message, proposal } of approvals) {
    let current = work.value.find((item) => item.id === message.id);
    if (!current) {
      try { current = await getWorkItem(accountID, message.id); }
      catch (cause) {
        if (!isAPIProblem(cause) || cause.status !== 404) throw cause;
        try {
          current = await createWorkFromAgentMessage(accountID, message.id, { kind: "todo", title: proposal.title, description: `${proposal.description}\n\nThis is approved setup Work from the Business Baseline. Work on this task rather than continuing the onboarding interview.`, priority: proposal.priority, assignment: { responsibility: "shared" }, reason: `Approved during Business Baseline: ${proposal.key}` });
        } catch (createCause) {
          if (!isAPIProblem(createCause) || ![409, 412].includes(createCause.status)) throw createCause;
          current = await getWorkItem(accountID, message.id);
        }
      }
    }
    const linked = current.provenance.conversation_id || !message.conversation_id ? current : await linkWorkConversation(accountID, current, { conversation_id: message.conversation_id, reason: "Approved during the Business Baseline interview" });
    const assigned = linked.assignment.responsibility === "persona" && linked.assignment.persona_id === persona.value.id
      ? linked
      : await assignWork(accountID, linked, { assignment: { responsibility: "persona", persona_id: persona.value.id }, reason: "The owner approved this setup task for their Operations Guide" });
    work.value = [...work.value.filter((existing) => existing.id !== assigned.id), assigned];
  }
}

function continueToYourTurn(): void { allowNextNavigation(); void router.push("/app/your-turn"); }
watch(() => [session.selectedID, route.params.assessmentID, available.value, manageable.value], () => {
  const assessmentID = typeof route.params.assessmentID === "string" ? route.params.assessmentID : "";
  if (suppressedAssessmentRoute && suppressedAssessmentRoute === assessmentID) { suppressedAssessmentRoute = ""; return; }
  void load();
}, { immediate: true });
onBeforeUnmount(() => { pollGeneration += 1; if (pollTimer !== undefined) window.clearTimeout(pollTimer); });
</script>

<template>
  <section class="page baseline-interview-page">
    <p class="sr-only" role="status" aria-live="polite">{{ announcement }}</p>
    <header class="page-heading baseline-heading"><div><p class="eyebrow">Meet your operations agent</p><h1>Tell Spyglass how your business works.</h1><p>This is a conversation, not a form. Your agent will ask what matters to your business, remember your answers, and help set up useful work.</p></div><div v-if="interview" class="baseline-business-chip"><span>Business</span><strong>{{ interview.business_type }}</strong><small>{{ label(interview.business_type_confidence) }} confidence</small></div></header>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <section v-if="loading" class="queue-state" aria-busy="true"><h2>Opening your conversation…</h2></section>
    <section v-else-if="!available" class="queue-state queue-state--warning"><h2>Business Setup is not available</h2><p>This Account needs both Knowledge and Agents access.</p></section>
    <section v-else-if="error && !baseline" class="queue-state queue-state--error"><h2>Business Setup could not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></section>
    <section v-else-if="!baseline || messages.length === 0" class="baseline-welcome">
      <div class="baseline-agent-row"><span class="baseline-agent-avatar" aria-hidden="true">O</span><div class="baseline-agent-bubble"><p class="baseline-agent-name"><strong>Operations Guide</strong><span>Your first Spyglass agent</span></p><h2>Let’s start with your business—not a generic checklist.</h2><p>I’ll learn what you sell, how the work moves, what gets in your way, and where Spyglass can help. I’ll skip things that do not apply.</p><small>You can leave and return to this same conversation at any time.</small></div></div>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p><IoButton v-if="manageable" :disabled="sending || preparing" @click="begin">{{ sending || preparing ? "Getting your agent ready…" : "Start the conversation" }}</IoButton><p v-else class="queue-inline-status">An Owner or Administrator must start Business Setup.</p>
    </section>
    <div v-else class="baseline-conversation-layout">
      <section class="baseline-chat-card">
        <header class="baseline-chat-header"><div class="baseline-agent-avatar" aria-hidden="true">O</div><div><strong>{{ persona?.name ?? "Operations Guide" }}</strong><span>{{ runPending ? "Thinking about what to ask next…" : "Learning your business" }}</span></div></header>
        <ol ref="chat" class="baseline-chat-log" aria-label="Business Baseline conversation" tabindex="0"><li v-for="item in visibleMessages" :key="item.id" :class="messageClass(item)"><div><p class="baseline-message-meta"><strong>{{ messageName(item) }}</strong><time :datetime="item.created_at">{{ messageTime(item.created_at) }}</time></p><p class="baseline-message-body">{{ messageBody(item) }}</p></div></li><li v-if="runPending" class="baseline-message baseline-message--persona"><div><p class="baseline-message-meta"><strong>{{ persona?.name ?? "Operations Guide" }}</strong></p><p class="baseline-thinking"><span></span><span></span><span></span><b class="sr-only">Thinking</b></p></div></li></ol>
        <section v-if="runNeedsAttention" class="baseline-run-recovery" role="alert"><div><p class="eyebrow">Your answer is safe</p><h2>{{ invalidModelOutput ? "Your agent could not prepare a valid reply." : "Your agent could not finish this reply." }}</h2><p>Nothing needs to be retyped. Try again and Spyglass will reuse the same answer without adding it twice.</p></div><IoButton kind="secondary" :disabled="recovering" @click="retryFailedRun">{{ recovering ? "Trying again…" : "Try again" }}</IoButton></section>
        <section v-if="availableOffers.length && !ready && !runNeedsAttention" class="baseline-offers" aria-labelledby="baseline-offers-title"><div><p class="eyebrow">A useful next step</p><h2 id="baseline-offers-title">Your agent sees a place Spyglass can help.</h2></div><article v-for="offer in availableOffers" :key="offer.key"><div><strong>{{ offer.title }}</strong><p>{{ offer.description }}</p></div><IoButton kind="secondary" :disabled="sending || runPending" @click="acceptOffer(offer)">Yes, add this</IoButton></article><p class="form-note">Suggestions do not turn into Work until you say yes.</p></section>
        <form v-if="!ready && !runNeedsAttention" class="baseline-composer" @submit.prevent="submitReply"><label for="baseline-reply">Your reply</label><textarea id="baseline-reply" v-model="reply" rows="3" maxlength="4000" placeholder="Type your answer here…" required @keydown="handleReplyKeydown" /><div><small>Enter sends · Shift+Enter adds a new line. Your answer is saved to this Account’s Knowledge. Do not include passwords or private customer data.</small><IoButton type="submit" :disabled="sending || runPending || !reply.trim()">{{ sending || runPending ? "Working…" : "Send" }}</IoButton></div></form>
        <section v-if="ready" class="baseline-finish"><p class="eyebrow">Baseline established</p><h2>Your agent has enough to start helping.</h2><p>{{ interview?.readiness_reason }}</p><IoButton @click="continueToYourTurn">Continue to Your Turn</IoButton></section><p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      </section>
      <aside class="baseline-notebook" aria-label="What Spyglass has learned"><p class="eyebrow">Built as you talk</p><h2>Your business notebook</h2><dl><div><dt>Answers saved</dt><dd>{{ capturedTopicCount }}</dd></div><div><dt>Setup work created</dt><dd>{{ baselineWork.length }}</dd></div></dl><section v-if="interview?.captured_topics.length"><h3>What we understand</h3><ul><li v-for="topic in interview.captured_topics" :key="topic">{{ topic }}</li></ul></section><section v-if="interview?.missing_topics.length && !ready"><h3>Still worth learning</h3><ul><li v-for="topic in interview.missing_topics" :key="topic">{{ topic }}</li></ul></section><section v-if="baselineWork.length"><h3>Work added</h3><ul><li v-for="item in baselineWork" :key="item.id"><RouterLink :to="`/app/work/${item.id}`">{{ item.title }}</RouterLink></li></ul></section><p class="form-note">Only your replies become confirmed Knowledge. Suggestions become Work only after you approve them.</p></aside>
    </div>
  </section>
</template>

<style scoped>
.baseline-heading{display:flex;justify-content:space-between;gap:2rem;align-items:flex-end}.baseline-heading>div:first-child{max-width:780px}.baseline-business-chip{min-width:220px;padding:1rem 1.1rem;border:1px solid #c8d7d5;border-radius:18px;background:#fff;display:grid;gap:.15rem}.baseline-business-chip span,.baseline-business-chip small{font-size:.78rem;color:#526765}.baseline-welcome{max-width:820px;display:grid;gap:1.25rem}.baseline-conversation-layout{display:grid;grid-template-columns:minmax(0,1fr) 300px;gap:1.4rem;align-items:start}.baseline-chat-card,.baseline-notebook{border:1px solid #c8d7d5;border-radius:22px;background:#fff;overflow:hidden}.baseline-chat-header{display:flex;gap:.75rem;align-items:center;padding:1rem 1.2rem;border-bottom:1px solid #dbe5e3}.baseline-chat-header>div:last-child{display:grid}.baseline-chat-header span{font-size:.82rem;color:#526765}.baseline-chat-log{list-style:none;margin:0;padding:1.25rem;display:grid;gap:1rem;max-height:52vh;overflow:auto}.baseline-message{display:flex}.baseline-message>div{max-width:min(78%,680px);padding:.85rem 1rem;border-radius:18px;background:#f2f6f5}.baseline-message--user{justify-content:flex-end}.baseline-message--user>div{background:#073f48;color:#fff;border-bottom-right-radius:5px}.baseline-message--persona>div{border-bottom-left-radius:5px}.baseline-message--approval>div{padding:.5rem .75rem;border-radius:999px;background:#e3f2ed;color:#07565c}.baseline-message--approval .baseline-message-meta{display:none}.baseline-message--approval .baseline-message-body{font-size:.86rem;font-weight:700;line-height:1.35}.baseline-message-meta{display:flex;gap:.7rem;align-items:center;margin:0 0 .35rem;font-size:.75rem;opacity:.76}.baseline-message-body{margin:0;white-space:pre-wrap;line-height:1.55}.baseline-thinking{display:flex;gap:.25rem;margin:.3rem 0}.baseline-thinking span{width:7px;height:7px;border-radius:50%;background:#087f88;animation:pulse 1.2s infinite}.baseline-thinking span:nth-child(2){animation-delay:.15s}.baseline-thinking span:nth-child(3){animation-delay:.3s}@keyframes pulse{0%,80%,100%{opacity:.25}40%{opacity:1}}.baseline-composer,.baseline-offers,.baseline-finish,.baseline-run-recovery{border-top:1px solid #dbe5e3;padding:1.1rem 1.25rem}.baseline-composer{display:grid;gap:.55rem}.baseline-composer textarea{width:100%;resize:vertical}.baseline-composer>div{display:flex;gap:1rem;align-items:center;justify-content:space-between}.baseline-composer small{color:#526765}.baseline-offers{display:grid;gap:.8rem;background:#fbf7ed}.baseline-offers h2{font-size:1.15rem;margin:.1rem 0}.baseline-offers article{display:flex;gap:1rem;justify-content:space-between;align-items:center}.baseline-offers article p{margin:.2rem 0}.baseline-run-recovery{display:flex;justify-content:space-between;align-items:center;gap:1rem;background:#fff5e6}.baseline-run-recovery h2{font-size:1.15rem;margin:.1rem 0}.baseline-run-recovery p:last-child{margin:.25rem 0 0}.baseline-notebook{padding:1.25rem;position:sticky;top:1rem}.baseline-notebook h2{font-size:1.35rem}.baseline-notebook dl{display:grid;grid-template-columns:1fr 1fr;gap:.65rem}.baseline-notebook dl div{padding:.8rem;border-radius:14px;background:#f2f6f5;display:grid}.baseline-notebook dt{font-size:.75rem;color:#526765}.baseline-notebook dd{font-size:1.4rem;font-weight:700;margin:0}.baseline-notebook section{border-top:1px solid #dbe5e3;margin-top:1rem;padding-top:.8rem}.baseline-notebook h3{font-size:.9rem}.baseline-notebook ul{padding-left:1.1rem}.baseline-notebook li{margin:.4rem 0}.baseline-finish{background:#edf7f3}.baseline-finish>.eyebrow{color:#06656c}.baseline-finish h2{margin:.2rem 0}.baseline-agent-avatar{flex:0 0 auto;width:42px;height:42px;border-radius:50%;display:grid;place-items:center;background:#087f88;color:#fff;font-weight:800}
@media (max-width:800px){.baseline-heading{display:block}.baseline-business-chip{margin-top:1rem}.baseline-conversation-layout{grid-template-columns:1fr}.baseline-notebook{position:static;order:-1}.baseline-chat-log{max-height:none}.baseline-message>div{max-width:92%}.baseline-composer>div,.baseline-offers article,.baseline-run-recovery{align-items:stretch;flex-direction:column}.baseline-heading h1{font-size:clamp(2.2rem,12vw,3.5rem)}}
</style>
