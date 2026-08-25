<script setup lang="ts">
import {
  APIProblem,
  assignWork,
  createWork,
  getWorkItem,
  getWorkSummary,
  listWork,
  listWorkChildren,
  transitionWork,
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
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const route = useRoute();
const router = useRouter();
const items = ref<ReadonlyArray<WorkItem>>([]);
const summary = ref<WorkSummary>();
const detail = ref<WorkItem>();
const children = ref<ReadonlyArray<WorkItem>>([]);
const loading = ref(false);
const detailLoading = ref(false);
const saving = ref(false);
const error = ref("");
const detailError = ref("");
const announcement = ref("");
const nextCursor = ref("");
const search = ref("");
const state = ref<"" | WorkState>("");
const kind = ref<"" | WorkKind>("");
const createOpen = ref(false);
const transitionOpen = ref(false);
const assignmentOpen = ref(false);
const transitionTarget = ref<WorkState>("in_progress");
const transitionReason = ref("");
const assignmentResponsibility = ref<"user" | "shared" | "external">("shared");
const assignmentExternal = ref("");
const assignmentReason = ref("");
const draft = reactive({ title: "", description: "", kind: "ticket" as WorkKind, priority: "normal" as WorkPriority, responsibility: "shared" as "user" | "shared" | "external", external: "" });
let listSequence = 0;
let detailSequence = 0;

const workPackage = computed(() => session.selected?.entitlements.packages.find((value) => value.code === "work"));
const available = computed(() => Boolean(workPackage.value && workPackage.value.mode !== "suspended"));
const writable = computed(() => Boolean(workPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected.role)));
const itemID = computed(() => typeof route.params.itemID === "string" ? route.params.itemID : "");

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

function label(value: string): string {
  return ({ todo: "To-do", in_progress: "In progress" } as Record<string, string>)[value] ?? value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase());
}

function assignment(value: WorkItem): string {
  if (value.assignment.responsibility === "user") return value.assignment.user_id === session.userID || !value.assignment.user_id ? "Assigned to you" : "Assigned person";
  if (value.assignment.responsibility === "external") return value.assignment.external_ref ?? "External owner";
  if (value.assignment.responsibility === "persona") return "Agent Persona";
  return "Shared responsibility";
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
  if (!accountID || !itemID.value || !available.value) { detail.value = undefined; children.value = []; return; }
  const sequence = ++detailSequence;
  detailLoading.value = true;
  detailError.value = "";
  try {
    const [item, childPage] = await Promise.all([getWorkItem(accountID, itemID.value), listWorkChildren(accountID, itemID.value)]);
    if (sequence !== detailSequence) return;
    detail.value = item;
    children.value = childPage.items;
  } catch (cause) {
    if (sequence !== detailSequence) return;
    detailError.value = cause instanceof APIProblem ? cause.message : "Work detail is unavailable right now.";
  } finally { if (sequence === detailSequence) detailLoading.value = false; }
}

async function submitCreate(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || saving.value) return;
  saving.value = true; error.value = "";
  const workAssignment: WorkAssignmentInput = draft.responsibility === "external"
    ? { responsibility: "external", external_ref: draft.external.trim() }
    : { responsibility: draft.responsibility };
  try {
    const created = await createWork(accountID, { kind: draft.kind, title: draft.title.trim(), description: draft.description.trim(), priority: draft.priority, assignment: workAssignment });
    try { sessionStorage.removeItem(draftKey(accountID)); } catch { /* Optional storage. */ }
    Object.assign(draft, { title: "", description: "", kind: "ticket", priority: "normal", responsibility: "shared", external: "" });
    createOpen.value = false;
    announcement.value = `Created work item ${created.number}: ${created.title}.`;
    await refresh();
    await router.push(`/app/work/${encodeURIComponent(created.id)}`);
  } catch (cause) { error.value = cause instanceof APIProblem ? cause.message : "Work could not be created."; }
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
      detailError.value = "This work item changed. Spyglass loaded the current version; review it before trying again.";
      transitionOpen.value = false;
      await loadDetail();
    } else detailError.value = cause instanceof APIProblem ? cause.message : "The work state could not be changed.";
  } finally { saving.value = false; }
}

function beginAssignment(): void {
  if (!detail.value) return;
  assignmentResponsibility.value = detail.value.assignment.responsibility === "external" ? "external" : detail.value.assignment.responsibility === "user" ? "user" : "shared";
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
    : { responsibility: assignmentResponsibility.value };
  try {
    detail.value = await assignWork(accountID, detail.value, { assignment: workAssignment, reason: assignmentReason.value.trim() });
    assignmentOpen.value = false;
    announcement.value = `Responsibility updated for work item ${detail.value.number}.`;
    await refresh();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 412) { assignmentOpen.value = false; await loadDetail(); detailError.value = "This work item changed. Review the current assignment before trying again."; }
    else detailError.value = cause instanceof APIProblem ? cause.message : "Responsibility could not be updated.";
  } finally { saving.value = false; }
}

onMounted(restoreDraft);
watch(draft, saveDraft, { deep: true });
watch(() => [session.selectedID, available.value], () => { restoreDraft(); void refresh(); }, { immediate: true });
watch(() => [session.selectedID, itemID.value, available.value], () => void loadDetail(), { immediate: true });
</script>

<template>
  <section class="page work-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <template v-if="itemID">
      <RouterLink class="back-link" to="/app/work">← Back to Work</RouterLink>
      <section v-if="detailLoading" class="queue-state" role="status"><h1>Loading work…</h1></section>
      <section v-else-if="detailError && !detail" class="queue-state queue-state--error" role="alert"><h1>Work did not load</h1><p>{{ detailError }}</p><IoButton kind="secondary" @click="loadDetail">Try again</IoButton></section>
      <template v-else-if="detail">
        <header class="detail-heading"><div><p class="eyebrow">{{ label(detail.kind) }} · #{{ String(detail.number).padStart(4, "0") }}</p><h1>{{ detail.title }}</h1></div><span class="state-badge">{{ label(detail.state) }}</span></header>
        <p v-if="detailError" class="queue-inline-status queue-inline-status--error" role="alert">{{ detailError }}</p>
        <div class="detail-layout">
          <article class="detail-card work-detail-card">
            <h2>Outcome and context</h2><p class="work-description">{{ detail.description || "No description has been added." }}</p>
            <dl><div><dt>Priority</dt><dd>{{ label(detail.priority) }}</dd></div><div><dt>Responsibility</dt><dd>{{ assignment(detail) }}</dd></div><div><dt>Origin</dt><dd>{{ label(detail.provenance.source) }}</dd></div><div><dt>Due</dt><dd>{{ date(detail.due_at) }}</dd></div><div><dt>Version</dt><dd>{{ detail.version }}</dd></div></dl>
            <section class="work-children"><h2>Direct work · {{ children.length }}</h2><p v-if="children.length === 0">No direct child items.</p><RouterLink v-for="child in children" :key="child.id" :to="`/app/work/${child.id}`" class="work-child-link"><strong>#{{ child.number }} · {{ child.title }}</strong><span>{{ label(child.state) }}</span></RouterLink></section>
          </article>
          <aside class="decision-card">
            <h2>Move this work</h2>
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
      <header class="page-heading page-heading--action"><div><p class="eyebrow">Account commitments</p><h1>Work</h1><p>Human commitments, Agent activity and operational follow-through in one Account-scoped queue.</p></div><IoButton v-if="writable" @click="createOpen = !createOpen">{{ createOpen ? "Close new work" : "New work" }}</IoButton></header>
      <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Work always belongs to one Account.</p></section>
      <section v-else-if="!available" class="queue-state"><h2>Work is not enabled</h2><p>This Account's current package set does not include Work.</p><a href="/app#billing">Review Account plans</a></section>
      <template v-else>
        <form v-if="createOpen" class="work-create decision-card" @submit.prevent="submitCreate">
          <h2>Create a clear next step</h2>
          <label>Title<input v-model="draft.title" maxlength="240" required placeholder="What needs to happen?"></label>
          <label>Description<textarea v-model="draft.description" maxlength="20000" rows="4" placeholder="Outcome, context and definition of done"></textarea></label>
          <div class="work-form-grid"><label>Type<select v-model="draft.kind"><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><label>Priority<select v-model="draft.priority"><option value="normal">Normal</option><option value="high">High</option><option value="urgent">Urgent</option><option value="low">Low</option></select></label><label>Responsibility<select v-model="draft.responsibility"><option value="shared">Shared</option><option value="user">Assign to me</option><option value="external">External owner</option></select></label></div>
          <label v-if="draft.responsibility === 'external'">External owner reference<input v-model="draft.external" minlength="2" maxlength="200" required></label>
          <p class="form-note">This unsubmitted draft stays only in this browser tab.</p><p v-if="error" class="form-error" role="alert">{{ error }}</p><IoButton type="submit" :disabled="saving">{{ saving ? "Creating…" : "Create work" }}</IoButton>
        </form>
        <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Add a passkey and save recovery codes before creating or changing Work.</p><a href="/app/security?return_to=%2Fapp%2Fwork">Continue security setup</a></section>
        <div v-if="summary" class="work-summary" aria-label="Work summary"><article><small>Active</small><strong>{{ summary.active }}</strong></article><article><small>In progress</small><strong>{{ summary.in_progress }}</strong></article><article><small>Waiting</small><strong>{{ summary.waiting }}</strong></article><article><small>Urgent</small><strong>{{ summary.urgent }}</strong></article></div>
        <form class="work-filters" aria-label="Filter work" @submit.prevent="refresh()"><label><span>Search</span><input v-model="search" type="search" maxlength="200" placeholder="Title or description"></label><label><span>State</span><select v-model="state"><option value="">All states</option><option value="open">Open</option><option value="in_progress">In progress</option><option value="waiting">Waiting</option><option value="done">Done</option><option value="canceled">Canceled</option></select></label><label><span>Type</span><select v-model="kind"><option value="">All types</option><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><IoButton type="submit" kind="secondary" :disabled="loading">Apply</IoButton></form>
        <section v-if="loading && items.length === 0" class="queue-state" role="status"><h2>Loading Account work…</h2></section>
        <section v-else-if="error && items.length === 0" class="queue-state queue-state--error" role="alert"><h2>Work did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="refresh()">Try again</IoButton></section>
        <section v-else-if="items.length === 0" class="queue-state"><h2>No work matches this view</h2><p>Adjust the filters or create the first clear next step.</p></section>
        <template v-else><p v-if="error" class="queue-inline-status queue-inline-status--error">{{ error }} Showing the last loaded queue.</p><ol class="work-list"><li v-for="item in items" :key="item.id"><RouterLink :to="`/app/work/${item.id}`" class="work-card"><div class="card-meta"><span>#{{ String(item.number).padStart(4, "0") }} · {{ label(item.kind) }}</span><span>{{ label(item.state) }}</span></div><h2>{{ item.title }}</h2><p>{{ assignment(item) }} · {{ label(item.priority) }} · {{ date(item.due_at) }}</p></RouterLink></li></ol><IoButton v-if="nextCursor" class="load-more" kind="secondary" :disabled="loading" @click="refresh(true)">{{ loading ? "Loading…" : "Load more" }}</IoButton></template>
      </template>
    </template>

    <div v-if="transitionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="transition-title" @submit.prevent="submitTransition"><h2 id="transition-title">{{ label(transitionTarget) }} this work?</h2><label>Operational reason<textarea v-model="transitionReason" minlength="3" maxlength="1000" rows="4" required></textarea></label><div class="modal-actions"><IoButton type="button" kind="secondary" @click="transitionOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : "Confirm change" }}</IoButton></div></form></div>
    <div v-if="assignmentOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="assignment-title" @submit.prevent="submitAssignment"><h2 id="assignment-title">Set responsibility</h2><label>Responsibility<select v-model="assignmentResponsibility"><option value="shared">Shared</option><option value="user">Assign to me</option><option value="external">External owner</option></select></label><label v-if="assignmentResponsibility === 'external'">External reference<input v-model="assignmentExternal" minlength="2" maxlength="200" required></label><label>Reason<textarea v-model="assignmentReason" minlength="3" maxlength="1000" rows="3" required></textarea></label><div class="modal-actions"><IoButton type="button" kind="secondary" @click="assignmentOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving">{{ saving ? "Saving…" : "Update responsibility" }}</IoButton></div></form></div>
  </section>
</template>
