<script setup lang="ts">
import {
  APIProblem,
  createSchedule,
  deleteSchedule,
  getSchedule,
  listAgentBoardrooms,
  listAgentPersonas,
  listSchedules,
  pauseSchedule,
  resumeSchedule,
  reviseSchedule,
  triggerSchedule,
  type AgentBoardroom,
  type AgentPersona,
  type CreateScheduleRequest,
  type Schedule,
  type ScheduleFrequency,
  type ScheduleGapPolicy,
  type ScheduleMissedRunPolicy,
  type ScheduleOverlapPolicy
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, reactive, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";
import ScheduleFields from "../components/ScheduleFields.vue";

type ScheduleAction = "pause" | "resume" | "trigger" | "delete";
type ScheduleDraft = {
  name: string; timezone: string; frequency: ScheduleFrequency; localHour: number; localMinute: number;
  weekdays: number[]; gapPolicy: ScheduleGapPolicy; overlapPolicy: ScheduleOverlapPolicy;
  missedRunPolicy: ScheduleMissedRunPolicy; boardroomID: string; mode: "selected" | "manager_led";
  personaIDs: string[]; subject: string; prompt: string; workItemIDs: string; factIDs: string;
  documentIDs: string; assessmentIDs: string; reason: string;
};

const session = useSessionStore();
const route = useRoute();
const router = useRouter();
const schedules = ref<ReadonlyArray<Schedule>>([]);
const nextCursor = ref("");
const detail = ref<Schedule>();
const boardrooms = ref<ReadonlyArray<AgentBoardroom>>([]);
const personas = ref<ReadonlyArray<AgentPersona>>([]);
const optionsLoading = ref(false);
const optionsError = ref("");
const loading = ref(false);
const detailLoading = ref(false);
const saving = ref(false);
const error = ref("");
const detailError = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const editorOpen = ref(false);
const actionOpen = ref(false);
const action = ref<ScheduleAction>("pause");
const actionReason = ref("");
const deletionConfirmed = ref(false);
let listSequence = 0;
let detailSequence = 0;

const defaultTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
const emptyDraft = (): ScheduleDraft => ({
  name: "", timezone: defaultTimezone, frequency: "weekly", localHour: 9, localMinute: 0, weekdays: [1],
  gapPolicy: "next_valid", overlapPolicy: "first", missedRunPolicy: "catch_up_one", boardroomID: "",
  mode: "selected", personaIDs: [], subject: "", prompt: "", workItemIDs: "", factIDs: "",
  documentIDs: "", assessmentIDs: "", reason: ""
});
const draft = reactive<ScheduleDraft>(emptyDraft());

const agentsPackage = computed(() => session.selected?.entitlements.packages.find((value) => value.code === "agents"));
const available = computed(() => Boolean(agentsPackage.value && agentsPackage.value.mode !== "suspended"));
const writable = computed(() => Boolean(agentsPackage.value?.mode === "enabled" && session.selected && !session.selected.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected.role)));
const scheduleID = computed(() => typeof route.params.scheduleID === "string" ? route.params.scheduleID : "");
const editing = computed(() => Boolean(scheduleID.value && detail.value));
const definitionReady = computed(() => Boolean(draft.boardroomID && draft.personaIDs.length));
const hasUnsavedScheduleWork = computed(() => editorOpen.value || actionOpen.value);
const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedScheduleWork,
  pending: saving,
  message: "Leave Schedules? Your open definition or command will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Schedule change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Schedule definition or command remains open.";
  }
});
const weekdayOptions = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function date(value: string | null): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Not currently scheduled"; }
function time(value: Schedule): string {
  const minute = String(value.recurrence.local_minute).padStart(2, "0");
  const at = `${String(value.recurrence.local_hour).padStart(2, "0")}:${minute}`;
  if (value.recurrence.frequency === "daily") return `Daily at ${at}`;
  return `${(value.recurrence.weekdays ?? []).map((day) => weekdayOptions[day]?.slice(0, 3)).join(", ")} at ${at}`;
}
function splitIDs(value: string): ReadonlyArray<string> | null {
  const ids = value.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean);
  return ids.length ? [...new Set(ids)] : null;
}
function boardroomName(id: string): string { return boardrooms.value.find((item) => item.id === id)?.name ?? id; }
function personaNames(ids: ReadonlyArray<string>): string {
  return ids.map((id) => personas.value.find((item) => item.id === id)?.name ?? id).join(", ");
}
function draftKey(accountID: string): string { return `spyglass_schedule_draft:${accountID}`; }
function restoreDraft(): void {
  if (scheduleID.value) return;
  Object.assign(draft, emptyDraft());
  const accountID = session.selectedID;
  if (!accountID) return;
  try {
    const saved = JSON.parse(sessionStorage.getItem(draftKey(accountID)) ?? "{}") as Partial<ScheduleDraft> & { personaIDs?: string[] | string };
    Object.assign(draft, saved, { personaIDs: Array.isArray(saved.personaIDs) ? saved.personaIDs : typeof saved.personaIDs === "string" ? splitIDs(saved.personaIDs) ?? [] : [] });
  } catch { /* Optional tab storage. */ }
}
function saveDraft(): void {
  const accountID = session.selectedID;
  if (!accountID || scheduleID.value) return;
  try { sessionStorage.setItem(draftKey(accountID), JSON.stringify(draft)); } catch { /* Optional tab storage. */ }
}
function request(): CreateScheduleRequest {
  return {
    name: draft.name.trim(), timezone: draft.timezone.trim(), missed_run_policy: draft.missedRunPolicy,
    recurrence: {
      frequency: draft.frequency, local_hour: Number(draft.localHour), local_minute: Number(draft.localMinute),
      weekdays: draft.frequency === "weekly" ? [...draft.weekdays].sort() : null,
      gap_policy: draft.gapPolicy, overlap_policy: draft.overlapPolicy
    },
    template: {
      boardroom_id: draft.boardroomID.trim(), mode: draft.mode, persona_ids: [...new Set(draft.personaIDs)],
      subject: draft.subject.trim(), prompt: draft.prompt.trim(), work_item_ids: splitIDs(draft.workItemIDs),
      knowledge_fact_ids: splitIDs(draft.factIDs), knowledge_document_ids: splitIDs(draft.documentIDs),
      baseline_assessment_ids: splitIDs(draft.assessmentIDs)
    },
    reason: draft.reason.trim()
  };
}
function fillDraft(value: Schedule): void {
  Object.assign(draft, {
    name: value.name, timezone: value.timezone, frequency: value.recurrence.frequency,
    localHour: value.recurrence.local_hour, localMinute: value.recurrence.local_minute,
    weekdays: [...(value.recurrence.weekdays ?? [])], gapPolicy: value.recurrence.gap_policy,
    overlapPolicy: value.recurrence.overlap_policy, missedRunPolicy: value.missed_run_policy,
    boardroomID: value.template.boardroom_id, mode: value.template.mode,
    personaIDs: [...value.template.persona_ids], subject: value.template.subject, prompt: value.template.prompt,
    workItemIDs: value.template.work_item_ids?.join("\n") ?? "", factIDs: value.template.knowledge_fact_ids?.join("\n") ?? "",
    documentIDs: value.template.knowledge_document_ids?.join("\n") ?? "", assessmentIDs: value.template.baseline_assessment_ids?.join("\n") ?? "", reason: ""
  });
}

async function loadBoardroomOptions(): Promise<void> {
  const accountID = session.selectedID;
  boardrooms.value = []; personas.value = []; optionsError.value = "";
  if (!accountID || !available.value) return;
  optionsLoading.value = true;
  try {
    boardrooms.value = await listAgentBoardrooms(accountID);
    if (draft.boardroomID) await loadPersonaOptions(accountID, draft.boardroomID);
  } catch (cause) {
    optionsError.value = cause instanceof APIProblem ? cause.message : "Agent teams are unavailable right now.";
  } finally { optionsLoading.value = false; }
}

async function loadPersonaOptions(accountID: string, boardroomID: string): Promise<void> {
  optionsLoading.value = true; optionsError.value = ""; personas.value = [];
  try {
    const values = await listAgentPersonas(accountID, boardroomID);
    personas.value = values.filter((item) => item.state === "active" || draft.personaIDs.includes(item.id));
    const availableIDs = new Set(personas.value.map((item) => item.id));
    draft.personaIDs = draft.personaIDs.filter((id) => availableIDs.has(id));
  } catch (cause) {
    optionsError.value = cause instanceof APIProblem ? cause.message : "Agents are unavailable right now.";
  } finally { optionsLoading.value = false; }
}

async function refresh(append = false): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !available.value) { schedules.value = []; nextCursor.value = ""; return; }
  const sequence = ++listSequence; loading.value = true; error.value = "";
  try {
    const page = await listSchedules(accountID, append ? nextCursor.value : undefined);
    if (sequence !== listSequence) return;
    schedules.value = append ? [...schedules.value, ...page.items] : page.items;
    nextCursor.value = page.next_cursor ?? "";
  } catch (cause) { if (sequence === listSequence) error.value = cause instanceof APIProblem ? cause.message : "Schedules are unavailable right now."; }
  finally { if (sequence === listSequence) loading.value = false; }
}
async function loadDetail(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !scheduleID.value || !available.value) { detail.value = undefined; return; }
  const sequence = ++detailSequence; detailLoading.value = true; detailError.value = ""; editorOpen.value = false;
  try { const value = await getSchedule(accountID, scheduleID.value); if (sequence === detailSequence) detail.value = value; }
  catch (cause) { if (sequence === detailSequence) detailError.value = cause instanceof APIProblem ? cause.message : "This schedule is unavailable right now."; }
  finally { if (sequence === detailSequence) detailLoading.value = false; }
}
function beginEdit(): void { if (detail.value) { fillDraft(detail.value); editorOpen.value = true; } }
async function submitDefinition(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || saving.value) return;
  saving.value = true; navigationNotice.value = "";
  if (editing.value) detailError.value = "";
  else error.value = "";
  try {
    if (editing.value && detail.value) {
      detail.value = await reviseSchedule(accountID, detail.value, request());
      editorOpen.value = false; announcement.value = `${detail.value.name} updated.`;
    } else {
      const created = await createSchedule(accountID, request());
      try { sessionStorage.removeItem(draftKey(accountID)); } catch { /* Optional tab storage. */ }
      Object.assign(draft, emptyDraft()); editorOpen.value = false; announcement.value = `${created.name} scheduled.`;
      await refresh(); allowNextNavigation(); await router.push(`/app/schedules/${encodeURIComponent(created.id)}`);
    }
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 409 && editing.value) {
      editorOpen.value = false; await loadDetail(); detailError.value = "This schedule changed. Review the current version before editing again.";
    } else if (editing.value) detailError.value = cause instanceof APIProblem ? cause.message : "The schedule could not be updated.";
    else error.value = cause instanceof APIProblem ? cause.message : "The schedule could not be created.";
  } finally { saving.value = false; }
}
function beginAction(value: ScheduleAction): void { action.value = value; actionReason.value = ""; deletionConfirmed.value = false; actionOpen.value = true; }
async function submitAction(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || !detail.value || saving.value) return;
  saving.value = true; detailError.value = ""; navigationNotice.value = "";
  try {
    const current = detail.value;
    if (action.value === "pause") detail.value = await pauseSchedule(accountID, current, actionReason.value.trim());
    if (action.value === "resume") detail.value = await resumeSchedule(accountID, current, actionReason.value.trim());
    if (action.value === "delete") detail.value = await deleteSchedule(accountID, current, actionReason.value.trim());
    if (action.value === "trigger") await triggerSchedule(accountID, current, actionReason.value.trim());
    actionOpen.value = false; announcement.value = action.value === "trigger" ? `${current.name} was queued to run now.` : `${current.name} is now ${detail.value?.state}.`;
    await refresh();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 409) { actionOpen.value = false; await loadDetail(); detailError.value = "This schedule changed. Review the current version before trying again."; }
    else detailError.value = cause instanceof APIProblem ? cause.message : "The schedule command could not be completed.";
  } finally { saving.value = false; }
}

watch(draft, saveDraft, { deep: true });
watch(() => [session.selectedID, available.value], () => { restoreDraft(); void refresh(); void loadBoardroomOptions(); }, { immediate: true });
watch(() => draft.boardroomID, (boardroomID) => {
  const accountID = session.selectedID;
  if (!accountID || !boardroomID) { personas.value = []; draft.personaIDs = []; return; }
  void loadPersonaOptions(accountID, boardroomID);
});
watch(() => [session.selectedID, scheduleID.value, available.value], () => void loadDetail(), { immediate: true });
</script>

<template>
  <section class="page schedules-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice && !actionOpen" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <template v-if="scheduleID">
      <RouterLink class="back-link" to="/app/schedules">← Back to Schedules</RouterLink>
      <section v-if="detailLoading" class="queue-state" role="status"><h1>Loading schedule…</h1></section>
      <section v-else-if="detailError && !detail" class="queue-state queue-state--error" role="alert"><h1>Schedule did not load</h1><p>{{ detailError }}</p><IoButton kind="secondary" @click="loadDetail">Try again</IoButton></section>
      <template v-else-if="detail">
        <header class="detail-heading"><div><h1>{{ detail.name }}</h1></div><span class="state-badge">{{ label(detail.state) }}</span></header>
        <p v-if="detailError && !editorOpen && !actionOpen" class="queue-inline-status queue-inline-status--error" role="alert">{{ detailError }}</p>
        <form v-if="editorOpen" class="schedule-editor decision-card" @submit.prevent="submitDefinition"><h2>Edit schedule</h2><ScheduleFields :model="draft" :boardrooms="boardrooms" :personas="personas" :options-loading="optionsLoading" :options-error="optionsError" @update="Object.assign(draft, $event)" /><p v-if="detailError" class="form-error" role="alert">{{ detailError }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="editorOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving || optionsLoading || Boolean(optionsError) || !definitionReady">{{ saving ? "Saving…" : "Save schedule" }}</IoButton></div></form>
        <div v-else class="detail-layout">
          <article class="detail-card schedule-detail"><h2>When it runs</h2><dl><div><dt>Recurrence</dt><dd>{{ time(detail) }}</dd></div><div><dt>Timezone</dt><dd>{{ detail.timezone }}</dd></div><div><dt>Next run</dt><dd>{{ date(detail.next_run_at) }}</dd></div><div><dt>Daylight-saving gap</dt><dd>{{ label(detail.recurrence.gap_policy) }}</dd></div><div><dt>Repeated local time</dt><dd>{{ label(detail.recurrence.overlap_policy) }}</dd></div><div><dt>Missed run</dt><dd>{{ label(detail.missed_run_policy) }}</dd></div><div><dt>Version</dt><dd>{{ detail.version }}</dd></div></dl><details><summary>Task details</summary><dl><div><dt>Subject</dt><dd>{{ detail.template.subject }}</dd></div><div><dt>Mode</dt><dd>{{ label(detail.template.mode) }}</dd></div><div><dt>Agent team</dt><dd>{{ boardroomName(detail.template.boardroom_id) }}</dd></div><div><dt>Agents</dt><dd>{{ personaNames(detail.template.persona_ids) }}</dd></div></dl><p>{{ detail.template.prompt }}</p></details></article>
          <aside class="decision-card"><h2>Schedule controls</h2><p v-if="!writable" class="form-note">Your current package or role provides read-only access.</p><template v-else-if="detail.state !== 'deleted'"><IoButton kind="secondary" @click="beginEdit">Edit definition</IoButton><IoButton v-if="detail.state === 'active'" kind="secondary" @click="beginAction('trigger')">Run now</IoButton><IoButton v-if="detail.state === 'active'" kind="secondary" @click="beginAction('pause')">Pause</IoButton><IoButton v-if="detail.state === 'paused'" kind="secondary" @click="beginAction('resume')">Resume</IoButton><IoButton kind="secondary" @click="beginAction('delete')">Delete schedule</IoButton></template><p v-else class="form-note">Deleted schedules remain in the durable audit record and cannot be changed.</p></aside>
        </div>
      </template>
    </template>

    <template v-else>
      <header class="page-heading page-heading--action"><div><h1>Schedules</h1><p>Schedule recurring conversations with your agents.</p></div><IoButton v-if="writable" @click="editorOpen = !editorOpen">{{ editorOpen ? "Close" : "New schedule" }}</IoButton></header>
      <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Schedules always belong to one Account.</p></section>
      <section v-else-if="!available" class="queue-state"><h2>Schedules are not enabled</h2><p>Schedules are part of the Agents package for this Account.</p><a href="/app#billing">Review Account plans</a></section>
      <template v-else>
        <form v-if="editorOpen" class="schedule-editor decision-card" @submit.prevent="submitDefinition"><h2>Create a schedule</h2><ScheduleFields :model="draft" :boardrooms="boardrooms" :personas="personas" :options-loading="optionsLoading" :options-error="optionsError" @update="Object.assign(draft, $event)" /><p v-if="error" class="form-error" role="alert">{{ error }}</p><IoButton type="submit" :disabled="saving || optionsLoading || Boolean(optionsError) || !definitionReady">{{ saving ? "Creating…" : "Create schedule" }}</IoButton></form>
        <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Finish two-factor authentication before changing Schedules.</p><a href="/app/security?return_to=%2Fapp%2Fschedules">Continue security setup</a></section>
        <section v-if="loading && schedules.length === 0" class="queue-state" role="status"><h2>Loading schedules…</h2></section>
        <section v-else-if="error && schedules.length === 0" class="queue-state queue-state--error" role="alert"><h2>Schedules did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="refresh()">Try again</IoButton></section>
        <section v-else-if="schedules.length === 0" class="queue-state"><h2>No schedules yet</h2><p>Add a schedule to run an agent task automatically.</p></section>
        <template v-else><p v-if="error" class="queue-inline-status queue-inline-status--error">{{ error }} Showing the last saved information.</p><ol class="schedule-list"><li v-for="item in schedules" :key="item.id"><RouterLink class="schedule-card" :to="`/app/schedules/${item.id}`"><div><span class="state-badge">{{ label(item.state) }}</span><h2>{{ item.name }}</h2><p>{{ time(item) }} · {{ item.timezone }}</p></div><small>Next: {{ date(item.next_run_at) }}</small></RouterLink></li></ol><IoButton v-if="nextCursor" class="load-more" kind="secondary" :disabled="loading" @click="refresh(true)">{{ loading ? "Loading…" : "Load more" }}</IoButton></template>
      </template>
    </template>

    <div v-if="actionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="schedule-action-title" @submit.prevent="submitAction"><h2 id="schedule-action-title">{{ action === 'trigger' ? 'Run this schedule now?' : `${label(action)} this schedule?` }}</h2><p class="form-note">This change will be saved in the schedule history.</p><label>Reason for this change<textarea v-model="actionReason" minlength="3" maxlength="500" rows="4" required></textarea></label><label v-if="action === 'delete'" class="confirmation"><input v-model="deletionConfirmed" type="checkbox" required><span>I understand this removes the schedule from future execution.</span></label><p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p><p v-if="detailError" class="form-error" role="alert">{{ detailError }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="actionOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving || (action === 'delete' && !deletionConfirmed)">{{ saving ? "Saving…" : "Confirm" }}</IoButton></div></form></div>
  </section>
</template>
