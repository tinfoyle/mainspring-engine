<script setup lang="ts">
import {
  APIProblem,
  configureAgentManager,
  createAgentBoardroom,
  listAgentBoardrooms,
  listAgentConversations,
  listAgentPersonas,
  publishAgentPersona,
  type AgentBoardroom,
  type AgentConversation,
  type AgentPersona,
  type PublishAgentPersonaRequest
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, reactive, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useConversationStore } from "../stores/conversation";
import PersonaEditor from "../components/PersonaEditor.vue";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const chat = useConversationStore();
async function openWorkspaceChat(): Promise<void> { const id = roomID.value; const failure = await router.push("/app/workspace"); if (!failure) { await chat.open(id); chat.mobileChat = true; } }
const route = useRoute();
const router = useRouter();
const rooms = ref<ReadonlyArray<AgentBoardroom>>([]);
const personas = ref<ReadonlyArray<AgentPersona>>([]);
const conversations = ref<ReadonlyArray<AgentConversation>>([]);
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
let loadSequence = 0;

const agentsPackage = computed(() => session.selected?.entitlements.packages.find((value) => value.code === "agents"));
const available = computed(() => Boolean(agentsPackage.value && agentsPackage.value.mode !== "suspended"));
const configurable = computed(() => Boolean(agentsPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator"].includes(session.selected.role)));
const runnable = computed(() => Boolean(agentsPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected.role)));
const roomID = computed(() => typeof route.params.roomID === "string" ? route.params.roomID : "");
const room = computed(() => rooms.value.find((value) => value.id === roomID.value));
const activePersonas = computed(() => personas.value.filter((value) => value.state === "active"));

const hasRoomDraft = computed(() => createOpen.value && Boolean(roomDraft.name.trim() || roomDraft.purpose.trim()));
const hasManagerDraft = computed(() => Boolean(room.value && managerPersonaID.value && managerPersonaID.value !== (room.value.manager_persona_id ?? "")));
const hasUnsavedAgentWork = computed(() => hasRoomDraft.value || hasManagerDraft.value || personaDirty.value);
const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedAgentWork,
  pending: saving,
  message: "Leave Agents? Your unsaved changes will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Agent change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your changes are still here.";
  }
});

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function initials(value: string): string { return value.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join("").toUpperCase(); }
function date(value: string): string { return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
function personaComplexity(persona?: AgentPersona): "simple" | "efficient" | "balanced" | "thorough" | "advanced" {
  const value = persona?.policy.complexity;
  return typeof value === "string" && ["simple", "efficient", "balanced", "thorough", "advanced"].includes(value) ? value as "simple" | "efficient" | "balanced" | "thorough" | "advanced" : "balanced";
}

async function loadRooms(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !available.value) { rooms.value = []; return; }
  const sequence = ++loadSequence;
  loading.value = true; error.value = "";
  try { const values = await listAgentBoardrooms(accountID); if (sequence === loadSequence) rooms.value = values; }
  catch (cause) { if (sequence === loadSequence) error.value = cause instanceof APIProblem ? cause.message : "Agent teams are unavailable right now."; }
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
  } catch (cause) { roomError.value = cause instanceof APIProblem ? cause.message : "This agent team is unavailable right now."; }
  finally { roomLoading.value = false; }
}

async function createRoom(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !configurable.value || saving.value) return;
  saving.value = true; error.value = ""; navigationNotice.value = "";
  try {
    const created = await createAgentBoardroom(accountID, { name: roomDraft.name.trim(), purpose: roomDraft.purpose.trim() });
    Object.assign(roomDraft, { name: "", purpose: "" }); createOpen.value = false;
    announcement.value = `Agent team ${created.name} created.`; await loadRooms(); allowNextNavigation(); await router.push(`/app/agents/boardrooms/${created.id}`);
  } catch (cause) { error.value = cause instanceof APIProblem ? cause.message : "The agent team could not be created."; }
  finally { saving.value = false; }
}

async function saveManager(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !configurable.value || !room.value || !managerPersonaID.value || saving.value) return;
  saving.value = true; roomError.value = ""; navigationNotice.value = "";
  try {
    const updated = await configureAgentManager(accountID, room.value.id, { manager_persona_id: managerPersonaID.value, expected_version: room.value.version });
    rooms.value = rooms.value.map((value) => value.id === updated.id ? updated : value);
    await chat.refreshTeam(updated.id);
    announcement.value = `${personas.value.find((value) => value.id === managerPersonaID.value)?.name ?? "This agent"} will now summarize the other agents’ answers.`;
  } catch (cause) { roomError.value = cause instanceof APIProblem && cause.status === 409 ? "The team changed. Refresh it before choosing the summary agent." : cause instanceof APIProblem ? cause.message : "The summary agent could not be saved."; }
  finally { saving.value = false; }
}

function openPersona(persona?: AgentPersona): void { editingPersona.value = persona; roomError.value = ""; personaDirty.value = false; personaOpen.value = true; }
function closePersona(): void { personaOpen.value = false; editingPersona.value = undefined; personaDirty.value = false; }
async function publishPersona(input: PublishAgentPersonaRequest): Promise<void> {
  const accountID = session.selectedID; if (!accountID || !configurable.value || !room.value || saving.value) return; saving.value = true; roomError.value = ""; navigationNotice.value = "";
  try { const value = await publishAgentPersona(accountID, room.value.id, input); closePersona(); announcement.value = `${value.name} version ${value.latest_version} published.`; await loadRoom(); await chat.refreshTeam(room.value.id); }
  catch (cause) { if (cause instanceof APIProblem && cause.status === 409) { closePersona(); await loadRoom(); roomError.value = "This agent changed. Review its current version before saving again."; } else roomError.value = cause instanceof APIProblem ? cause.message : "The agent version could not be saved."; }
  finally { saving.value = false; }
}

watch(() => [session.selectedID, available.value], () => void loadRooms(), { immediate: true });
watch(() => [session.selectedID, roomID.value, room.value?.version, available.value], () => void loadRoom(), { immediate: true });
</script>

<template>
  <section class="page agents-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <template v-if="!roomID">
      <header class="page-heading page-heading--action"><div><h1>Agents &amp; tools</h1><p>Create agents, give them instructions, and work with them in shared conversations.</p></div><IoButton v-if="configurable" @click="createOpen = !createOpen">{{ createOpen ? "Close" : "New agent team" }}</IoButton></header>
      <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Organize agents into teams for different jobs.</p></section>
      <section v-else-if="!available" class="queue-state"><h2>Agents is not enabled</h2><p>This Account's current package set does not include Agents.</p><a href="/app/billing">Review Account plans</a></section>
      <template v-else>
        <form v-if="createOpen && configurable" class="decision-card agents-create" @submit.prevent="createRoom"><h2>Create an agent team</h2><label>Name<input v-model="roomDraft.name" minlength="2" maxlength="160" required></label><label>Purpose<textarea v-model="roomDraft.purpose" maxlength="2000" rows="4"></textarea></label><p v-if="error" class="form-error" role="alert">{{ error }}</p><IoButton type="submit" :disabled="saving">{{ saving ? "Creating…" : "Create agent team" }}</IoButton></form>
        <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Set up two-step verification before changing or running agents.</p><a href="/app/security?return_to=%2Fapp%2Fagents">Continue security setup</a></section>
        <section v-if="loading" class="queue-state" role="status"><h2>Loading agent teams…</h2></section>
        <section v-else-if="error && rooms.length === 0" class="queue-state queue-state--error" role="alert"><h2>Agent teams did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="loadRooms">Try again</IoButton></section>
        <section v-else-if="rooms.length === 0" class="queue-state"><h2>No agent teams yet</h2><p>Create a team, add agents, then give them a task.</p></section>
        <ol v-else class="agents-room-list"><li v-for="value in rooms" :key="value.id"><RouterLink :to="`/app/agents/boardrooms/${value.id}`" class="agents-room-card"><span class="agents-avatar">{{ initials(value.name) }}</span><div><strong>{{ value.name }}</strong><p>{{ value.purpose || "No purpose has been recorded." }}</p><small>{{ label(value.state) }} · policy version {{ value.version }}</small></div></RouterLink></li></ol>
      </template>
    </template>

    <template v-else>
      <RouterLink class="back-link" to="/app/agents">← All agent teams</RouterLink>
      <section v-if="loading || (roomLoading && !room)" class="queue-state" role="status"><h1>Loading agent team…</h1></section>
      <section v-else-if="!room" class="queue-state queue-state--error"><h1>Agent team not found</h1><p>{{ error || "This agent team is not available in this account." }}</p></section>
      <template v-else>
        <header class="detail-heading agents-heading"><div><p class="eyebrow">Agent team · version {{ room.version }}</p><h1>{{ room.name }}</h1><p>{{ room.purpose }}</p></div><span class="state-badge">{{ label(room.state) }}</span></header>
        <p v-if="roomError" class="queue-inline-status queue-inline-status--error" role="alert">{{ roomError }}</p>
        <section class="agents-roster">
          <header><div><h2>Agents</h2></div><div><span>{{ activePersonas.length }} active</span><IoButton v-if="configurable" @click="openPersona()">New agent</IoButton></div></header><div v-if="personas.length === 0" class="queue-state"><h3>No agents yet</h3><p>Add an agent before starting a conversation.</p></div><div v-else class="agents-personas"><details v-for="persona in personas" :key="persona.id" class="agents-persona"><summary><span class="agents-avatar">{{ initials(persona.name) }}</span><span><strong>{{ persona.name }}</strong><small>{{ persona.role }} · version {{ persona.latest_version }}</small></span></summary><p>{{ persona.description }}</p><dl><div><dt>Complexity</dt><dd>{{ label(personaComplexity(persona)) }}</dd></div><div><dt>Citations</dt><dd>{{ label(persona.policy.citation_policy) }}</dd></div><div><dt>Actions</dt><dd>{{ persona.policy.action_policy === "propose" ? "Ask for approval first" : "None" }}</dd></div><div><dt>Tools</dt><dd>{{ persona.policy.tools.length }}</dd></div><div><dt>Digest</dt><dd><code>{{ persona.content_digest.slice(0, 16) }}…</code></dd></div></dl><IoButton v-if="configurable && persona.state === 'active'" kind="secondary" @click="openPersona(persona)">Save new version</IoButton></details></div>
          <form v-if="configurable && activePersonas.length" class="agents-manager" @submit.prevent="saveManager"><label>Summary agent<select v-model="managerPersonaID" required><option v-for="persona in activePersonas" :key="persona.id" :value="persona.id">{{ persona.name }} · v{{ persona.latest_version }}</option></select></label><IoButton type="submit" kind="secondary" :disabled="saving">Save summary agent</IoButton></form>
        </section>

        <button v-if="runnable && activePersonas.length" type="button" class="workspace-open-chat" @click="openWorkspaceChat">Open chat with this team</button>

        <section class="agents-conversations"><header><div><h2>Conversations</h2></div><span>{{ conversations.length }}</span></header><div v-if="conversations.length === 0" class="queue-state"><h3>No conversations yet</h3><p>Ask the agents a question to start a conversation.</p></div><ol v-else><li v-for="item in conversations" :key="item.id"><RouterLink :to="`/app/agents/boardrooms/${room.id}/conversations/${item.id}`" class="agents-conversation-card"><strong>{{ item.subject }}</strong><span>{{ item.message_count }} {{ item.message_count === 1 ? "message" : "messages" }} · {{ label(item.state) }}</span><small>{{ date(item.updated_at) }}</small></RouterLink></li></ol></section>
      </template>
    </template>
    <PersonaEditor v-if="personaOpen && configurable" :persona="editingPersona" :saving="saving" :error="roomError" @close="closePersona" @dirty-change="personaDirty = $event" @publish="publishPersona" />
  </section>
</template>
