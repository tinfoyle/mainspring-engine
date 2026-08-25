<script setup lang="ts">
import {
  APIProblem,
  configureAgentManager,
  createAgentBoardroom,
  getAgentConversation,
  getAgentRun,
  listAgentBoardrooms,
  listAgentConversations,
  listAgentMessages,
  listAgentPersonas,
  publishAgentPersona,
  resolveAgentRun,
  startAgentRun,
  type AgentBoardroom,
  type AgentConversation,
  type AgentMessage,
  type AgentPersona,
  type AgentRun,
  type AgentRunMode,
  type AgentRunResolutionAction,
  type PublishAgentPersonaRequest
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import PersonaEditor from "../components/PersonaEditor.vue";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const route = useRoute();
const router = useRouter();
const rooms = ref<ReadonlyArray<AgentBoardroom>>([]);
const personas = ref<ReadonlyArray<AgentPersona>>([]);
const conversations = ref<ReadonlyArray<AgentConversation>>([]);
const conversation = ref<AgentConversation>();
const messages = ref<ReadonlyArray<AgentMessage>>([]);
const activeRun = ref<AgentRun>();
const loading = ref(false);
const roomLoading = ref(false);
const saving = ref(false);
const error = ref("");
const roomError = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const createOpen = ref(false);
const personaOpen = ref(false);
const personaDirty = ref(false);
const editingPersona = ref<AgentPersona>();
const roomDraft = reactive({ name: "", purpose: "" });
const managerPersonaID = ref("");
const subject = ref("");
const prompt = ref("");
const mode = ref<AgentRunMode>("selected");
const selectedPersonaIDs = ref<string[]>([]);
const recoveryAction = ref<AgentRunResolutionAction>("retry_failed");
const recoveryNote = ref("");
let loadSequence = 0;
let pollTimer: number | undefined;
let pollGeneration = 0;

const agentsPackage = computed(() => session.selected?.entitlements.packages.find((value) => value.code === "agents"));
const available = computed(() => Boolean(agentsPackage.value && agentsPackage.value.mode !== "suspended"));
const configurable = computed(() => Boolean(agentsPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator"].includes(session.selected.role)));
const runnable = computed(() => Boolean(agentsPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected.role)));
const roomID = computed(() => typeof route.params.roomID === "string" ? route.params.roomID : "");
const conversationID = computed(() => typeof route.params.conversationID === "string" ? route.params.conversationID : "");
const room = computed(() => rooms.value.find((value) => value.id === roomID.value));
const activePersonas = computed(() => personas.value.filter((value) => value.state === "active"));
const selectablePersonas = computed(() => activePersonas.value.filter((value) => mode.value !== "manager_led" || value.id !== room.value?.manager_persona_id));
const terminalRun = computed(() => activeRun.value && ["succeeded", "partially_failed", "failed", "canceled"].includes(activeRun.value.state));
const recoverableRun = computed(() => activeRun.value && ["partially_failed", "failed"].includes(activeRun.value.state) && activeRun.value.resolutions.length === 0);
const hasRoomDraft = computed(() => createOpen.value && Boolean(roomDraft.name.trim() || roomDraft.purpose.trim()));
const hasManagerDraft = computed(() => Boolean(room.value && managerPersonaID.value && managerPersonaID.value !== (room.value.manager_persona_id ?? "")));
const hasRunDraft = computed(() => {
  if (!room.value) return false;
  const expectedPersonas = selectablePersonas.value.map((value) => value.id).sort().join(":");
  const selectedPersonas = [...selectedPersonaIDs.value].sort().join(":");
  return Boolean(subject.value.trim() || prompt.value.trim() || mode.value !== "selected" || selectedPersonas !== expectedPersonas);
});
const hasRecoveryDraft = computed(() => Boolean(recoverableRun.value && (recoveryAction.value !== "retry_failed" || recoveryNote.value.trim())));
const hasUnsavedAgentWork = computed(() => hasRoomDraft.value || hasManagerDraft.value || hasRunDraft.value || hasRecoveryDraft.value || personaDirty.value);

const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedAgentWork,
  pending: saving,
  message: "Leave Agents? Your unsubmitted Boardroom or Persona changes will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Agent change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Boardroom or Persona changes remain ready for review.";
  }
});

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function initials(value: string): string { return value.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase(); }
function date(value: string): string { return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
function personaName(message: AgentMessage): string { return personas.value.find((value) => value.persona_version_id === message.persona_version_id)?.name ?? "Boardroom Persona"; }

async function loadRooms(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !available.value) { rooms.value = []; return; }
  const sequence = ++loadSequence;
  loading.value = true; error.value = "";
  try { const values = await listAgentBoardrooms(accountID); if (sequence === loadSequence) rooms.value = values; }
  catch (cause) { if (sequence === loadSequence) error.value = cause instanceof APIProblem ? cause.message : "Agent Boardrooms are unavailable right now."; }
  finally { if (sequence === loadSequence) loading.value = false; }
}

async function loadRoom(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !roomID.value || !available.value) { personas.value = []; conversations.value = []; return; }
  roomLoading.value = true; roomError.value = "";
  try {
    const [personaValues, conversationValues] = await Promise.all([listAgentPersonas(accountID, roomID.value), listAgentConversations(accountID, roomID.value)]);
    personas.value = personaValues; conversations.value = conversationValues;
    managerPersonaID.value = room.value?.manager_persona_id ?? "";
    selectedPersonaIDs.value = personaValues.filter((value) => value.state === "active").map((value) => value.id);
  } catch (cause) { roomError.value = cause instanceof APIProblem ? cause.message : "This Boardroom is unavailable right now."; }
  finally { roomLoading.value = false; }
}

async function loadConversation(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !conversationID.value || !available.value) { conversation.value = undefined; messages.value = []; activeRun.value = undefined; return; }
  roomError.value = "";
  try {
    const [current, projected] = await Promise.all([getAgentConversation(accountID, conversationID.value), listAgentMessages(accountID, conversationID.value)]);
    conversation.value = current; messages.value = projected;
    const latestRunID = [...projected].reverse().find((value) => value.run_id)?.run_id;
    if (latestRunID) activeRun.value = await getAgentRun(accountID, latestRunID);
  } catch (cause) { roomError.value = cause instanceof APIProblem ? cause.message : "This conversation is unavailable right now."; }
}

async function createRoom(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !configurable.value || saving.value) return;
  saving.value = true; error.value = ""; navigationNotice.value = "";
  try {
    const created = await createAgentBoardroom(accountID, { name: roomDraft.name.trim(), purpose: roomDraft.purpose.trim() });
    Object.assign(roomDraft, { name: "", purpose: "" }); createOpen.value = false;
    announcement.value = `Boardroom ${created.name} created.`; await loadRooms(); allowNextNavigation(); await router.push(`/app/agents/boardrooms/${created.id}`);
  } catch (cause) { error.value = cause instanceof APIProblem ? cause.message : "The Boardroom could not be created."; }
  finally { saving.value = false; }
}

async function saveManager(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !configurable.value || !room.value || !managerPersonaID.value || saving.value) return;
  saving.value = true; roomError.value = ""; navigationNotice.value = "";
  try {
    const updated = await configureAgentManager(accountID, room.value.id, { manager_persona_id: managerPersonaID.value, expected_version: room.value.version });
    rooms.value = rooms.value.map((value) => value.id === updated.id ? updated : value);
    announcement.value = `${personas.value.find((value) => value.id === managerPersonaID.value)?.name ?? "Persona"} is now the synthesis manager.`;
  } catch (cause) { roomError.value = cause instanceof APIProblem && cause.status === 409 ? "The Boardroom changed. Refresh it before setting the manager." : cause instanceof APIProblem ? cause.message : "The synthesis manager could not be set."; }
  finally { saving.value = false; }
}

function openPersona(persona?: AgentPersona): void { editingPersona.value = persona; roomError.value = ""; personaDirty.value = false; personaOpen.value = true; }
function closePersona(): void { personaOpen.value = false; editingPersona.value = undefined; personaDirty.value = false; }
async function publishPersona(input: PublishAgentPersonaRequest): Promise<void> {
  const accountID = session.selectedID; if (!accountID || !configurable.value || !room.value || saving.value) return; saving.value = true; roomError.value = ""; navigationNotice.value = "";
  try { const value = await publishAgentPersona(accountID, room.value.id, input); closePersona(); announcement.value = `${value.name} version ${value.latest_version} published.`; await loadRoom(); }
  catch (cause) { if (cause instanceof APIProblem && cause.status === 409) { closePersona(); await loadRoom(); roomError.value = "This Persona changed. Review its current immutable version before publishing again."; } else roomError.value = cause instanceof APIProblem ? cause.message : "The Persona version could not be published."; }
  finally { saving.value = false; }
}

async function runBoardroom(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !runnable.value || !room.value || saving.value || selectedPersonaIDs.value.length === 0) return;
  if (mode.value === "manager_led" && !room.value.manager_persona_id) { roomError.value = "Set a synthesis manager before starting a manager-led run."; return; }
  saving.value = true; roomError.value = ""; navigationNotice.value = "";
  const input: { prompt: string; mode: AgentRunMode; persona_ids: ReadonlyArray<string>; subject?: string; conversation_id?: string } = {
    prompt: prompt.value.trim(), mode: mode.value, persona_ids: selectedPersonaIDs.value
  };
  if (conversationID.value) input.conversation_id = conversationID.value;
  else input.subject = subject.value.trim();
  try {
    const run = await startAgentRun(accountID, room.value.id, input);
    activeRun.value = run; prompt.value = ""; subject.value = "";
    announcement.value = "The Boardroom run was accepted and is now governed by its frozen Persona versions and policy.";
    if (conversationID.value !== run.conversation_id) { allowNextNavigation(); await router.push(`/app/agents/boardrooms/${room.value.id}/conversations/${run.conversation_id}`); }
    beginPolling(run);
  } catch (cause) { roomError.value = cause instanceof APIProblem ? cause.message : "The Boardroom run could not be started."; }
  finally { saving.value = false; }
}

function beginPolling(run: AgentRun): void {
  if (pollTimer !== undefined) window.clearTimeout(pollTimer);
  const generation = ++pollGeneration;
  let attempt = 0;
  const poll = async (): Promise<void> => {
    const accountID = session.selectedID;
    if (!accountID || generation !== pollGeneration) return;
    try {
      const current = attempt === 0 ? run : await getAgentRun(accountID, run.id);
      if (generation !== pollGeneration) return;
      activeRun.value = current;
      if (["succeeded", "partially_failed", "failed", "canceled"].includes(current.state) || attempt >= 39) {
        await Promise.all([loadConversation(), loadRoom()]); return;
      }
      attempt += 1; pollTimer = window.setTimeout(() => void poll(), 1500);
    } catch (cause) { roomError.value = cause instanceof APIProblem ? `${cause.message} The durable run can be checked from this conversation.` : "The run could not be refreshed."; }
  };
  void poll();
}

async function resolveRun(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !runnable.value || !activeRun.value || saving.value) return;
  saving.value = true; roomError.value = ""; navigationNotice.value = "";
  try {
    const resolution = await resolveAgentRun(accountID, activeRun.value.id, { action: recoveryAction.value, note: recoveryNote.value.trim() });
    announcement.value = recoveryAction.value === "retry_failed" ? "Failed Persona turns were queued in a new immutable run." : "The failed run outcome was accepted.";
    recoveryNote.value = ""; recoveryAction.value = "retry_failed";
    if (resolution.retry_run_id) { const retry = await getAgentRun(accountID, resolution.retry_run_id); activeRun.value = retry; beginPolling(retry); }
    else activeRun.value = await getAgentRun(accountID, activeRun.value.id);
  } catch (cause) { roomError.value = cause instanceof APIProblem ? cause.message : "The run resolution could not be recorded."; }
  finally { saving.value = false; }
}

watch(mode, () => { const allowed = new Set(selectablePersonas.value.map((value) => value.id)); selectedPersonaIDs.value = selectedPersonaIDs.value.filter((value) => allowed.has(value)); if (selectedPersonaIDs.value.length === 0) selectedPersonaIDs.value = [...allowed]; });
watch(() => [session.selectedID, available.value], () => void loadRooms(), { immediate: true });
watch(() => [session.selectedID, roomID.value, room.value?.version, available.value], () => void loadRoom(), { immediate: true });
watch(() => [session.selectedID, conversationID.value, available.value], () => void loadConversation(), { immediate: true });
onBeforeUnmount(() => { pollGeneration += 1; if (pollTimer !== undefined) window.clearTimeout(pollTimer); });
</script>

<template>
  <section class="page agents-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <template v-if="!roomID">
      <header class="page-heading page-heading--action"><div><p class="eyebrow">Governed specialist collaboration</p><h1>Agents</h1><p>Convene versioned Personas in Account-scoped Boardrooms while consequential actions remain routed through Your Turn.</p></div><IoButton v-if="configurable" @click="createOpen = !createOpen">{{ createOpen ? "Close" : "New Boardroom" }}</IoButton></header>
      <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Every Boardroom belongs to one Account.</p></section>
      <section v-else-if="!available" class="queue-state"><h2>Agents is not enabled</h2><p>This Account's current package set does not include Agents.</p><a href="/app#billing">Review Account plans</a></section>
      <template v-else>
        <form v-if="createOpen && configurable" class="decision-card agents-create" @submit.prevent="createRoom"><h2>Create a Boardroom</h2><label>Name<input v-model="roomDraft.name" minlength="2" maxlength="160" required></label><label>Purpose<textarea v-model="roomDraft.purpose" maxlength="2000" rows="4"></textarea></label><p v-if="error" class="form-error" role="alert">{{ error }}</p><IoButton type="submit" :disabled="saving">{{ saving ? "Creating…" : "Create Boardroom" }}</IoButton></form>
        <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Add a passkey and save recovery codes before changing or running Boardrooms.</p><a href="/app/security?return_to=%2Fapp%2Fagents">Continue security setup</a></section>
        <section v-if="loading" class="queue-state" role="status"><h2>Loading Boardrooms…</h2></section>
        <section v-else-if="error && rooms.length === 0" class="queue-state queue-state--error" role="alert"><h2>Boardrooms did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="loadRooms">Try again</IoButton></section>
        <section v-else-if="rooms.length === 0" class="queue-state"><h2>No Boardrooms yet</h2><p>Create a focused operating room, then publish Personas through the governed Agent configuration boundary.</p></section>
        <ol v-else class="agents-room-list"><li v-for="value in rooms" :key="value.id"><RouterLink :to="`/app/agents/boardrooms/${value.id}`" class="agents-room-card"><span class="agents-avatar">{{ initials(value.name) }}</span><div><strong>{{ value.name }}</strong><p>{{ value.purpose || "No purpose has been recorded." }}</p><small>{{ label(value.state) }} · policy version {{ value.version }}</small></div></RouterLink></li></ol>
      </template>
    </template>

    <template v-else>
      <RouterLink class="back-link" to="/app/agents">← All Boardrooms</RouterLink>
      <section v-if="loading || (roomLoading && !room)" class="queue-state" role="status"><h1>Loading Boardroom…</h1></section>
      <section v-else-if="!room" class="queue-state queue-state--error"><h1>Boardroom not found</h1><p>{{ error || "This Boardroom is not available in the selected Account." }}</p></section>
      <template v-else>
        <header class="detail-heading agents-heading"><div><p class="eyebrow">Boardroom · policy version {{ room.version }}</p><h1>{{ room.name }}</h1><p>{{ room.purpose }}</p></div><span class="state-badge">{{ label(room.state) }}</span></header>
        <p v-if="roomError" class="queue-inline-status queue-inline-status--error" role="alert">{{ roomError }}</p>
        <section class="agents-roster">
          <header><div><p class="eyebrow">Frozen specialist definitions</p><h2>Personas</h2></div><div><span>{{ activePersonas.length }} active</span><IoButton v-if="configurable" @click="openPersona()">New Persona</IoButton></div></header><div v-if="personas.length === 0" class="queue-state"><h3>No published Personas</h3><p>A Boardroom needs at least one active, versioned Persona before it can run.</p></div><div v-else class="agents-personas"><details v-for="persona in personas" :key="persona.id" class="agents-persona"><summary><span class="agents-avatar">{{ initials(persona.name) }}</span><span><strong>{{ persona.name }}</strong><small>{{ persona.role }} · version {{ persona.latest_version }}</small></span></summary><p>{{ persona.description }}</p><dl><div><dt>Model policy</dt><dd>{{ persona.policy.provider }} · {{ persona.policy.model }}</dd></div><div><dt>Citations</dt><dd>{{ label(persona.policy.citation_policy) }}</dd></div><div><dt>Actions</dt><dd>{{ persona.policy.action_policy === "propose" ? "Propose only—human approval remains external" : "None" }}</dd></div><div><dt>Tools</dt><dd>{{ persona.policy.tools.length }}</dd></div><div><dt>Digest</dt><dd><code>{{ persona.content_digest.slice(0, 16) }}…</code></dd></div></dl><IoButton v-if="configurable && persona.state === 'active'" kind="secondary" @click="openPersona(persona)">Publish new version</IoButton></details></div>
          <form v-if="configurable && activePersonas.length" class="agents-manager" @submit.prevent="saveManager"><label>Synthesis manager<select v-model="managerPersonaID" required><option v-for="persona in activePersonas" :key="persona.id" :value="persona.id">{{ persona.name }} · v{{ persona.latest_version }}</option></select></label><IoButton type="submit" kind="secondary" :disabled="saving">Set manager</IoButton></form>
        </section>

        <form v-if="runnable && activePersonas.length" class="decision-card agents-composer" @submit.prevent="runBoardroom"><h2>{{ conversationID ? `Continue ${conversation?.subject ?? 'conversation'}` : "Convene this Boardroom" }}</h2><label v-if="!conversationID">Subject<input v-model="subject" minlength="2" maxlength="240" required placeholder="What needs a clear view?"></label><label>Your question<textarea v-model="prompt" maxlength="65536" rows="5" required placeholder="Context, constraints and the outcome you need"></textarea></label><label>Run mode<select v-model="mode"><option value="selected">Selected Personas</option><option value="manager_led" :disabled="!room.manager_persona_id">Specialists, then synthesis manager</option></select></label><fieldset><legend>Invite Personas</legend><label v-for="persona in selectablePersonas" :key="persona.id"><input v-model="selectedPersonaIDs" type="checkbox" :value="persona.id"> <span><strong>{{ persona.name }}</strong><small>{{ persona.role }} · v{{ persona.latest_version }}</small></span></label></fieldset><p class="form-note">Runs freeze exact Persona versions, policy and Account entitlements. Proposed consequential actions appear in Your Turn for human review.</p><IoButton type="submit" :disabled="saving || selectedPersonaIDs.length === 0">{{ saving ? "Convening…" : "Convene Boardroom" }}</IoButton></form>

        <section class="agents-conversations"><header><div><p class="eyebrow">Decision history</p><h2>Conversations</h2></div><span>{{ conversations.length }}</span></header><div v-if="conversations.length === 0" class="queue-state"><h3>No conversations yet</h3><p>Convene the Boardroom with a clear question.</p></div><ol v-else><li v-for="item in conversations" :key="item.id"><RouterLink :to="`/app/agents/boardrooms/${room.id}/conversations/${item.id}`" class="agents-conversation-card"><strong>{{ item.subject }}</strong><span>{{ item.message_count }} {{ item.message_count === 1 ? "message" : "messages" }} · {{ label(item.state) }}</span><small>{{ date(item.updated_at) }}</small></RouterLink></li></ol></section>

        <section v-if="conversationID" class="agents-transcript"><header><div><p class="eyebrow">Conversation</p><h2>{{ conversation?.subject ?? "Loading conversation…" }}</h2></div><span>{{ conversation ? label(conversation.state) : "Loading" }}</span></header><p v-if="activeRun" class="agents-run-state" role="status">Latest run: <strong>{{ label(activeRun.state) }}</strong><span v-if="!terminalRun"> · refreshing the durable projection</span></p><form v-if="recoverableRun && runnable" class="agents-recovery decision-card" @submit.prevent="resolveRun"><h3>Resolve this run</h3><p>Retrying creates a new immutable run from the original context and exact Persona versions. Accepting failure records the outcome without pretending it succeeded.</p><fieldset><legend>Resolution</legend><label><input v-model="recoveryAction" type="radio" value="retry_failed"> Retry failed turns</label><label><input v-model="recoveryAction" type="radio" value="accept_failure"> Accept failure</label></fieldset><label>Resolution note<textarea v-model="recoveryNote" minlength="3" maxlength="1000" rows="3" required></textarea></label><IoButton type="submit" :disabled="saving">Record resolution</IoButton></form><div class="agents-message-list" role="log" aria-label="Conversation messages"><article v-for="message in messages" :key="message.id" class="agents-message" :class="`agents-message--${message.role}`"><span class="agents-avatar">{{ message.role === "user" ? "YOU" : initials(personaName(message)) }}</span><div><header><strong>{{ message.role === "user" ? "You" : personaName(message) }}</strong><time :datetime="message.created_at">{{ date(message.created_at) }}</time></header><p>{{ message.body }}</p><template v-if="message.result"><section v-if="message.result.findings.length"><h3>Findings</h3><ul><li v-for="value in message.result.findings" :key="value">{{ value }}</li></ul></section><section v-if="message.result.recommendations.length"><h3>Recommendations</h3><ul><li v-for="value in message.result.recommendations" :key="value">{{ value }}</li></ul></section><section v-if="message.result.questions.length"><h3>Questions</h3><ul><li v-for="value in message.result.questions" :key="value">{{ value }}</li></ul></section><section v-if="message.result.citations.length"><h3>Citations</h3><ul><li v-for="citation in message.result.citations" :key="citation.id">{{ citation.label }}</li></ul></section><section v-if="message.result.proposed_actions.length" class="agents-proposals"><h3>Proposed actions</h3><ul><li v-for="action in message.result.proposed_actions" :key="`${action.kind}:${action.reason}`"><strong>{{ label(action.kind) }}</strong> · {{ action.reason }}</li></ul><RouterLink to="/app/your-turn">Review consequential proposals in Your Turn →</RouterLink></section><small class="agents-confidence">{{ label(message.result.confidence) }} confidence</small></template></div></article><p v-if="messages.length === 0" class="form-note">No projected messages yet.</p></div></section>
      </template>
    </template>
    <PersonaEditor v-if="personaOpen && configurable" :persona="editingPersona" :saving="saving" :error="roomError" @close="closePersona" @dirty-change="personaDirty = $event" @publish="publishPersona" />
  </section>
</template>
