<script setup lang="ts">
import {
  APIProblem,
  assignWork,
  createWork,
  getWorkItem,
  getWorkSummary,
  listAgentBoardrooms,
  listAgentMessages,
  listAgentPersonas,
  listWork,
  listWorkChildren,
  transitionWork,
  type AgentMessage,
  type AgentPersona,
  type WorkAssignmentInput,
  type WorkItem,
  type WorkKind,
  type WorkPriority,
  type WorkState,
  type WorkSummary
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onMounted, reactive, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useConversationStore } from "../stores/conversation";
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const chat = useConversationStore();
const route = useRoute();
const router = useRouter();
const items = ref<ReadonlyArray<WorkItem>>([]);
const summary = ref<WorkSummary>();
const detail = ref<WorkItem>();
const children = ref<ReadonlyArray<WorkItem>>([]);
const agentMessages = ref<ReadonlyArray<AgentMessage>>([]);
const agentActivityError = ref("");
const managerPersona = ref<AgentPersona>();
const loading = ref(false);
const detailLoading = ref(false);
const saving = ref(false);
const error = ref("");
const detailError = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const nextCursor = ref("");
const search = ref("");
const state = ref<"" | WorkState>("");
const kind = ref<"" | WorkKind>("");
const createOpen = ref(false);
const transitionOpen = ref(false);
const assignmentOpen = ref(false);
const transitionTarget = ref<WorkState>("in_progress");
const transitionReason = ref("");
const assignmentResponsibility = ref<"persona" | "user" | "shared" | "external">("persona");
const assignmentExternal = ref("");
const assignmentReason = ref("");
const draft = reactive({ title: "", description: "", kind: "ticket" as WorkKind, priority: "normal" as WorkPriority, responsibility: "persona" as "persona" | "user" | "external", external: "" });
let listSequence = 0;
let detailSequence = 0;

const workPackage = computed(() => session.selected?.entitlements.packages.find((value) => value.code === "work"));
const available = computed(() => Boolean(workPackage.value && workPackage.value.mode !== "suspended"));
const writable = computed(() => Boolean(workPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected.role)));
const itemID = computed(() => typeof route.params.itemID === "string" ? route.params.itemID : "");
const hasCreateDraft = computed(() => createOpen.value && Boolean(
  draft.title.trim()
  || draft.description.trim()
  || draft.kind !== "ticket"
  || draft.priority !== "normal"
  || draft.responsibility !== "persona"
  || draft.external.trim()
));
const hasTransitionDraft = computed(() => transitionOpen.value && Boolean(transitionReason.value.trim()));
const hasAssignmentDraft = computed(() => {
  if (!assignmentOpen.value || !detail.value) return false;
  const currentResponsibility = detail.value.assignment.responsibility;
  return Boolean(
    assignmentReason.value.trim()
    || assignmentResponsibility.value !== currentResponsibility
    || assignmentExternal.value.trim() !== (detail.value.assignment.external_ref ?? "")
  );
});
const hasUnsavedWork = computed(() => hasCreateDraft.value || hasTransitionDraft.value || hasAssignmentDraft.value);

const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedWork,
  pending: saving,
  message: "Leave Work? Your unsubmitted changes will remain only in this browser tab until you return.",
  onBlocked: (reason) => {
    navigationNotice.value = reason === "pending"
      ? "This Work change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Work changes remain on this page and in this browser tab.";
  }
});

const transitions = computed<ReadonlyArray<{ state: WorkState; label: string }>>(() => {
  if (!detail.value) return [];
  return ({
    open: [{ state: "in_progress", label: "Start" }, { state: "canceled", label: "Cancel" }],
    in_progress: [{ state: "waiting", label: "Mark waiting" }, { state: "done", label: "Complete" }, { state: "canceled", label: "Cancel" }],
    waiting: [{ state: "in_progress", label: "Resume" }, { state: "canceled", label: "Cancel" }],
    done: [{ state: "open", label: "Reopen" }],
    canceled: []
  } satisfies Record<WorkState, ReadonlyArray<{ state: WorkState; label: string }>>)[detail.value.state];
});
const latestAgentReply = computed(() => [...agentMessages.value].reverse().find((message) => message.role === "persona"));

function label(value: string): string {
  return ({ todo: "To-do", in_progress: "In progress" } as Record<string, string>)[value] ?? value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase());
}

function assignment(value: WorkItem): string {
  if (value.assignment.responsibility === "user") return value.assignment.user_id === session.userID || !value.assignment.user_id ? "Assigned to you" : "Assigned person";
  if (value.assignment.responsibility === "external") return value.assignment.external_ref ?? "External owner";
  const manager = managerPersona.value;
  if (value.assignment.responsibility === "persona") return manager && value.assignment.persona_id === manager.id ? manager.name : "Spyglass agent";
  return "Shared responsibility";
}

async function loadManagerPersona(): Promise<void> {
  const accountID = session.selectedID;
  managerPersona.value = undefined;
  if (!accountID) return;
  try {
    const rooms = (await listAgentBoardrooms(accountID)).filter((room) => room.state === "active" && room.manager_persona_id);
    for (const room of rooms) {
      const personas = await listAgentPersonas(accountID, room.id);
      const manager = personas.find((persona) => persona.id === room.manager_persona_id && persona.state === "active");
      if (manager) { managerPersona.value = manager; return; }
    }
  } catch { /* Work remains usable for explicit human assignment before an Agent is configured. */ }
}

function date(value?: string): string {
  if (!value) return "No due date";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(value));
}

function draftKey(accountID: string): string { return `spyglass.work.create.v2.${accountID}`; }

function restoreDraft(): void {
  if (!session.selectedID) return;
  try {
    const saved = JSON.parse(sessionStorage.getItem(draftKey(session.selectedID)) ?? "null") as Partial<typeof draft> | null;
    if (saved) Object.assign(draft, saved);
  } catch { /* Tab storage is optional. */ }
}

function saveDraft(): void {
  if (!session.selectedID) return;
  try { sessionStorage.setItem(draftKey(session.selectedID), JSON.stringify(draft)); } catch { /* Tab storage is optional. */ }
}

async function refresh(append = false): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !available.value) { items.value = []; summary.value = undefined; return; }
  const sequence = ++listSequence;
  loading.value = true;
  error.value = "";
  try {
    const filters: { search?: string; state?: WorkState; kind?: WorkKind; cursor?: string } = {};
    if (search.value.trim()) filters.search = search.value.trim();
    if (state.value) filters.state = state.value;
    if (kind.value) filters.kind = kind.value;
    if (append && nextCursor.value) filters.cursor = nextCursor.value;
    const [page, totals] = await Promise.all([
      listWork(accountID, filters),
      getWorkSummary(accountID)
    ]);
    if (sequence !== listSequence) return;
    items.value = append ? [...items.value, ...page.items] : page.items;
    nextCursor.value = page.next_cursor ?? "";
    summary.value = totals;
    if (append) announcement.value = `${page.items.length} more work items loaded.`;
  } catch (cause) {
    if (sequence !== listSequence) return;
    error.value = cause instanceof APIProblem ? cause.message : "Work is unavailable right now.";
  } finally { if (sequence === listSequence) loading.value = false; }
}

async function loadDetail(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !itemID.value || !available.value) { detail.value = undefined; children.value = []; agentMessages.value = []; return; }
  const sequence = ++detailSequence;
  detailLoading.value = true;
  detailError.value = "";
  agentActivityError.value = "";
  agentMessages.value = [];
  try {
    const [item, childPage] = await Promise.all([getWorkItem(accountID, itemID.value), listWorkChildren(accountID, itemID.value)]);
    if (sequence !== detailSequence) return;
    detail.value = item;
    children.value = childPage.items;
    if (item.provenance.conversation_id) {
      try {
        const projected = await listAgentMessages(accountID, item.provenance.conversation_id);
        if (sequence === detailSequence) agentMessages.value = projected;
      } catch {
        if (sequence === detailSequence) agentActivityError.value = "Agent progress could not be loaded right now.";
      }
    }
  } catch (cause) {
    if (sequence !== detailSequence) return;
    detailError.value = cause instanceof APIProblem ? cause.message : "Work detail is unavailable right now.";
  } finally { if (sequence === detailSequence) detailLoading.value = false; }
}

async function submitCreate(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || saving.value) return;
  saving.value = true; error.value = ""; navigationNotice.value = "";
  const workAssignment: WorkAssignmentInput = draft.responsibility === "external"
    ? { responsibility: "external", external_ref: draft.external.trim() }
    : draft.responsibility === "persona"
      ? { responsibility: "persona", persona_id: managerPersona.value?.id ?? "" }
      : { responsibility: "user" };
  try {
    if (draft.responsibility === "persona" && !managerPersona.value) throw new Error("Finish Business Setup before asking your operations agent to handle Work.");
    const created = await createWork(accountID, { kind: draft.kind, title: draft.title.trim(), description: draft.description.trim(), priority: draft.priority, assignment: workAssignment });
    try { sessionStorage.removeItem(draftKey(accountID)); } catch { /* Optional storage. */ }
    Object.assign(draft, { title: "", description: "", kind: "ticket", priority: "normal", responsibility: "persona", external: "" });
    createOpen.value = false;
    announcement.value = `Created work item ${created.number}: ${created.title}.`;
    await refresh();
    allowNextNavigation();
    await router.push(`/app/work/${encodeURIComponent(created.id)}`);
  } catch (cause) { error.value = cause instanceof APIProblem || cause instanceof Error ? cause.message : "Work could not be created."; }
  finally { saving.value = false; }
}

function beginTransition(target: WorkState): void { transitionTarget.value = target; transitionReason.value = ""; transitionOpen.value = true; }

async function submitTransition(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !detail.value || saving.value) return;
  saving.value = true; detailError.value = "";
  try {
    detail.value = await transitionWork(accountID, detail.value, { to: transitionTarget.value, reason: transitionReason.value.trim() });
    transitionOpen.value = false;
    announcement.value = `Work item ${detail.value.number} is now ${label(detail.value.state)}.`;
    await refresh();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 412) {
      transitionOpen.value = false;
      await loadDetail();
      detailError.value = "This work item changed. Spyglass loaded the current version; review it before trying again.";
    } else detailError.value = cause instanceof APIProblem ? cause.message : "The work state could not be changed.";
  } finally { saving.value = false; }
}

function beginAssignment(): void {
  if (!detail.value) return;
  assignmentResponsibility.value = detail.value.assignment.responsibility;
  assignmentExternal.value = detail.value.assignment.external_ref ?? "";
  assignmentReason.value = "";
  assignmentOpen.value = true;
}

async function submitAssignment(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !detail.value || saving.value) return;
  saving.value = true; detailError.value = "";
  const workAssignment: WorkAssignmentInput = assignmentResponsibility.value === "external"
    ? { responsibility: "external", external_ref: assignmentExternal.value.trim() }
    : assignmentResponsibility.value === "persona"
      ? { responsibility: "persona", persona_id: managerPersona.value?.id ?? "" }
      : { responsibility: assignmentResponsibility.value };
  try {
    if (assignmentResponsibility.value === "persona" && !managerPersona.value) throw new Error("Finish Business Setup before assigning Work to your operations agent.");
    detail.value = await assignWork(accountID, detail.value, { assignment: workAssignment, reason: assignmentReason.value.trim() });
    assignmentOpen.value = false;
    announcement.value = `Responsibility updated for work item ${detail.value.number}.`;
    await refresh();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 412) { assignmentOpen.value = false; await loadDetail(); detailError.value = "This work item changed. Review the current assignment before trying again."; }
    else detailError.value = cause instanceof APIProblem || cause instanceof Error ? cause.message : "Responsibility could not be updated.";
  } finally { saving.value = false; }
}

onMounted(restoreDraft);
watch(draft, saveDraft, { deep: true });
watch(() => [session.selectedID, available.value], () => { restoreDraft(); void refresh(); void loadManagerPersona(); }, { immediate: true });
watch(() => [session.selectedID, itemID.value, available.value], () => void loadDetail(), { immediate: true });
</script>

<template>
  <section class="page work-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <template v-if="itemID">
      <RouterLink class="back-link" to="/app/work">← Back to Work</RouterLink>
      <section v-if="detailLoading" class="queue-state" role="status"><h1>Loading work…</h1></section>
      <section v-else-if="detailError && !detail" class="queue-state queue-state--error" role="alert"><h1>Work did not load</h1><p>{{ detailError }}</p><IoButton kind="secondary" @click="loadDetail">Try again</IoButton></section>
      <template v-else-if="detail">
        <header class="detail-heading"><div><p class="eyebrow">{{ label(detail.kind) }} · #{{ String(detail.number).padStart(4, "0") }}</p><h1>{{ detail.title }}</h1></div><span class="state-badge">{{ label(detail.state) }}</span><button v-if="chat.available" type="button" @click="chat.attach({ kind: 'work', id: detail.id, title: detail.title, version: 'version ' + detail.version, text: JSON.stringify({ title: detail.title, description: detail.description, state: detail.state, id: detail.id }) })">Use in chat</button></header>
        <p v-if="detailError" class="queue-inline-status queue-inline-status--error" role="alert">{{ detailError }}</p>
        <div class="detail-layout">
          <article class="detail-card work-detail-card">
            <h2>Description</h2><p class="work-description">{{ detail.description || "No description has been added." }}</p>
            <dl><div><dt>Priority</dt><dd>{{ label(detail.priority) }}</dd></div><div><dt>Responsibility</dt><dd>{{ assignment(detail) }}</dd></div><div><dt>Origin</dt><dd>{{ label(detail.provenance.source) }}</dd></div><div><dt>Due</dt><dd>{{ date(detail.due_at) }}</dd></div><div><dt>Version</dt><dd>{{ detail.version }}</dd></div></dl>
            <section v-if="detail.assignment.responsibility === 'persona'" class="work-children" aria-labelledby="agent-progress-title">
              <h2 id="agent-progress-title">Agent progress</h2>
              <p v-if="agentActivityError" class="form-error">{{ agentActivityError }}</p>
              <template v-else-if="latestAgentReply">
                <p>{{ latestAgentReply.result?.contribution || latestAgentReply.body }}</p>
                <ul v-if="latestAgentReply.result?.findings.length"><li v-for="finding in latestAgentReply.result.findings" :key="finding">{{ finding }}</li></ul>
                <ul v-if="latestAgentReply.result?.recommendations.length"><li v-for="recommendation in latestAgentReply.result.recommendations" :key="recommendation">{{ recommendation }}</li></ul>
              </template>
              <p v-else-if="detail.state === 'waiting'">The agent needs an answer from you before it can continue.</p>
              <p v-else-if="detail.state === 'done'">The agent completed this work.</p>
              <p v-else-if="detail.provenance.run_id">The agent is working on this now.</p>
              <p v-else>This work is queued for the agent.</p>
              <RouterLink v-if="detail.state === 'waiting'" to="/app/your-turn" class="work-child-link"><strong>Answer in Your Turn</strong><span>Continue →</span></RouterLink>
            </section>
            <section class="work-children"><h2>Subtasks · {{ children.length }}</h2><p v-if="children.length === 0">No subtasks.</p><RouterLink v-for="child in children" :key="child.id" :to="`/app/work/${child.id}`" class="work-child-link"><strong>#{{ child.number }} · {{ child.title }}</strong><span>{{ label(child.state) }}</span></RouterLink></section>
          </article>
          <aside class="decision-card">
            <h2>Task actions</h2>
            <p v-if="!writable" class="form-note">Your current package or role provides read-only access.</p>
            <template v-else>
              <div class="work-action-row"><IoButton v-for="choice in transitions" :key="choice.state" :kind="choice.state === 'done' ? 'primary' : 'secondary'" @click="beginTransition(choice.state)">{{ choice.label }}</IoButton></div>
              <p v-if="transitions.length === 0" class="form-note">No lifecycle command is available from this state.</p>
              <IoButton kind="secondary" @click="beginAssignment">Edit responsibility</IoButton>
            </template>
          </aside>
        </div>
      </template>
    </template>

    <template v-else>
      <header class="page-heading page-heading--action"><div><h1>Work</h1><p>Track tasks assigned to you and your agents.</p></div><IoButton v-if="writable" @click="createOpen = !createOpen">{{ createOpen ? "Close new work" : "New work" }}</IoButton></header>
      <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Work always belongs to one Account.</p></section>
      <section v-else-if="!available" class="queue-state"><h2>Work is not enabled</h2><p>This Account's current package set does not include Work.</p><a href="/app/billing">Review Account plans</a></section>
      <template v-else>
        <form v-if="createOpen" class="work-create decision-card" @submit.prevent="submitCreate">
          <h2>New task</h2>
          <label>Title<input v-model="draft.title" maxlength="240" required placeholder="What needs to happen?"></label>
          <label>Description<textarea v-model="draft.description" maxlength="20000" rows="4" placeholder="Describe what needs to be done"></textarea></label>
          <div class="work-form-grid"><label>Type<select v-model="draft.kind"><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><label>Priority<select v-model="draft.priority"><option value="normal">Normal</option><option value="high">High</option><option value="urgent">Urgent</option><option value="low">Low</option></select></label><label>Who handles it?<select v-model="draft.responsibility"><option value="persona">My operations agent</option><option value="user">I will handle it</option><option value="external">Someone outside Spyglass</option></select></label></div>
          <label v-if="draft.responsibility === 'external'">External owner reference<input v-model="draft.external" minlength="2" maxlength="200" required></label>
          <p class="form-note">Agent work starts automatically. If the agent needs a fact or decision, it will appear in Your Turn. </p><p v-if="error" class="form-error" role="alert">{{ error }}</p><IoButton type="submit" :disabled="saving">{{ saving ? "Creating…" : "Create work" }}</IoButton>
        </form>
        <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Finish two-factor authentication before creating or changing Work.</p><a href="/app/security?return_to=%2Fapp%2Fwork">Continue security setup</a></section>
        <div v-if="summary" class="work-summary" role="group" aria-label="Work summary"><article><small>Active</small><strong>{{ summary.active }}</strong></article><article><small>In progress</small><strong>{{ summary.in_progress }}</strong></article><article><small>Waiting</small><strong>{{ summary.waiting }}</strong></article><article><small>Urgent</small><strong>{{ summary.urgent }}</strong></article></div>
        <form class="work-filters" aria-label="Filter work" @submit.prevent="refresh()"><label><span>Search</span><input v-model="search" type="search" maxlength="200" placeholder="Title or description"></label><label><span>State</span><select v-model="state"><option value="">All states</option><option value="open">Open</option><option value="in_progress">In progress</option><option value="waiting">Waiting</option><option value="done">Done</option><option value="canceled">Canceled</option></select></label><label><span>Type</span><select v-model="kind"><option value="">All types</option><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><IoButton type="submit" kind="secondary" :disabled="loading">Apply</IoButton></form>
        <section v-if="loading && items.length === 0" class="queue-state" role="status"><h2>Loading Account work…</h2></section>
        <section v-else-if="error && items.length === 0" class="queue-state queue-state--error" role="alert"><h2>Work did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="refresh()">Try again</IoButton></section>
        <section v-else-if="items.length === 0" class="queue-state"><h2>No work matches this view</h2><p>Adjust the filters or create the first clear next step.</p></section>
        <template v-else><p v-if="error" class="queue-inline-status queue-inline-status--error">{{ error }} Showing the last loaded queue.</p><ol class="work-list"><li v-for="item in items" :key="item.id"><RouterLink :to="`/app/work/${item.id}`" class="work-card"><div class="card-meta"><span>#{{ String(item.number).padStart(4, "0") }} · {{ label(item.kind) }}</span><span>{{ label(item.state) }}</span></div><h2>{{ item.title }}</h2><p>{{ assignment(item) }} · {{ label(item.priority) }} · {{ date(item.due_at) }}</p></RouterLink></li></ol><IoButton v-if="nextCursor" class="load-more" kind="secondary" :disabled="loading" @click="refresh(true)">{{ loading ? "Loading…" : "Load more" }}</IoButton></template>
      </template>
    </template>

    <div v-if="transitionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="transition-title" @submit.prevent="submitTransition"><h2 id="transition-title">{{ transitionTarget === 'in_progress' ? 'Start this task?' : transitionTarget === 'done' ? 'Complete this task?' : transitionTarget === 'waiting' ? 'Mark this task as waiting?' : transitionTarget === 'open' ? 'Reopen this task?' : 'Cancel this task?' }}</h2><label>Reason for this change<textarea v-model="transitionReason" minlength="3" maxlength="1000" rows="4" required></textarea></label><div class="modal-actions"><IoButton type="button" kind="secondary" @click="transitionOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : "Confirm change" }}</IoButton></div></form></div>
    <div v-if="assignmentOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="assignment-title" @submit.prevent="submitAssignment"><h2 id="assignment-title">Set responsibility</h2><label>Responsibility<select v-model="assignmentResponsibility"><option value="persona">My operations agent</option><option value="user">Assign to me</option><option value="shared">Shared</option><option value="external">External owner</option></select></label><label v-if="assignmentResponsibility === 'external'">External reference<input v-model="assignmentExternal" minlength="2" maxlength="200" required></label><label>Reason<textarea v-model="assignmentReason" minlength="3" maxlength="1000" rows="3" required></textarea></label><div class="modal-actions"><IoButton type="button" kind="secondary" @click="assignmentOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : "Update responsibility" }}</IoButton></div></form></div>
  </section>
</template>
