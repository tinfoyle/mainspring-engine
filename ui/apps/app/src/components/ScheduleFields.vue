<script setup lang="ts">
import { reactive, watch } from "vue";
import type { AgentBoardroom, AgentPersona, ScheduleFrequency, ScheduleGapPolicy, ScheduleMissedRunPolicy, ScheduleOverlapPolicy } from "@spyglass/api";

type Draft = {
  name: string; timezone: string; frequency: ScheduleFrequency; localHour: number; localMinute: number;
  weekdays: number[]; gapPolicy: ScheduleGapPolicy; overlapPolicy: ScheduleOverlapPolicy;
  missedRunPolicy: ScheduleMissedRunPolicy; boardroomID: string; mode: "selected" | "manager_led";
  personaIDs: string[]; subject: string; prompt: string; workItemIDs: string; factIDs: string;
  documentIDs: string; assessmentIDs: string; reason: string;
};

const props = defineProps<{
  model: Draft;
  boardrooms: ReadonlyArray<AgentBoardroom>;
  personas: ReadonlyArray<AgentPersona>;
  optionsLoading?: boolean;
  optionsError?: string;
}>();
const emit = defineEmits<{ update: [value: Draft] }>();
const fields = reactive<Draft>({ ...props.model, weekdays: [...props.model.weekdays], personaIDs: [...props.model.personaIDs] });
const weekdayOptions = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
let synchronizing = false;

watch(() => props.model, (value) => {
  synchronizing = true;
  Object.assign(fields, value, { weekdays: [...value.weekdays], personaIDs: [...value.personaIDs] });
  queueMicrotask(() => { synchronizing = false; });
}, { deep: true });
watch(fields, (value) => {
  if (!synchronizing) emit("update", { ...value, weekdays: [...value.weekdays], personaIDs: [...value.personaIDs] });
}, { deep: true });
</script>

<template>
  <label>Name<input v-model="fields.name" minlength="2" maxlength="160" required placeholder="Monday launch review"></label>
  <div class="schedule-grid"><label>Timezone<input v-model="fields.timezone" maxlength="100" required placeholder="America/New_York"></label><label>Frequency<select v-model="fields.frequency"><option value="daily">Daily</option><option value="weekly">Weekly</option></select></label><label>Hour<input v-model.number="fields.localHour" type="number" min="0" max="23" required></label><label>Minute<input v-model.number="fields.localMinute" type="number" min="0" max="59" required></label></div>
  <fieldset v-if="fields.frequency === 'weekly'" class="weekday-grid"><legend>Days</legend><label v-for="(weekday, index) in weekdayOptions" :key="weekday"><input v-model="fields.weekdays" type="checkbox" :value="index" :required="fields.weekdays.length === 0 && index === 0"> {{ weekday }}</label></fieldset>
  <details class="schedule-options"><summary>Timing behavior</summary><div class="schedule-grid"><label>Daylight-saving gap<select v-model="fields.gapPolicy"><option value="next_valid">Use next valid local time</option><option value="skip">Skip the run</option></select></label><label>Repeated local time<select v-model="fields.overlapPolicy"><option value="first">Use first occurrence</option><option value="second">Use second occurrence</option></select></label><label>Missed run<select v-model="fields.missedRunPolicy"><option value="catch_up_one">Catch up one run</option><option value="skip">Skip missed runs</option></select></label></div></details>
  <h3>Boardroom run</h3>
  <label>Boardroom<select v-model="fields.boardroomID" required><option value="">Choose a Boardroom</option><option v-for="boardroom in boardrooms" :key="boardroom.id" :value="boardroom.id" :disabled="boardroom.state !== 'active'">{{ boardroom.name }}{{ boardroom.state === "active" ? "" : " (archived)" }}</option></select></label>
  <fieldset class="schedule-personas"><legend>Personas</legend><p v-if="optionsLoading" class="form-note">Loading Personas…</p><p v-else-if="optionsError" class="form-error" role="alert">{{ optionsError }}</p><p v-else-if="!fields.boardroomID" class="form-note">Choose a Boardroom to see its Personas.</p><p v-else-if="personas.length === 0" class="form-note">This Boardroom has no published Personas yet.</p><label v-for="(persona, index) in personas" v-else :key="persona.id"><input v-model="fields.personaIDs" type="checkbox" :value="persona.id" :disabled="persona.state !== 'active'" :required="fields.personaIDs.length === 0 && index === 0"> <span><strong>{{ persona.name }}</strong><small>{{ persona.role }} · version {{ persona.latest_version }}{{ persona.state === "active" ? "" : ` · ${persona.state}` }}</small></span></label></fieldset>
  <label>Run mode<select v-model="fields.mode"><option value="selected">Selected Personas</option><option value="manager_led">Specialists, then synthesis manager</option></select></label><label>Subject<input v-model="fields.subject" minlength="2" maxlength="240" required></label><label>Prompt<textarea v-model="fields.prompt" maxlength="65536" rows="6" required></textarea></label>
  <details class="schedule-options"><summary>Optional Account context</summary><label>Work item IDs<textarea v-model="fields.workItemIDs" rows="2"></textarea></label><label>Knowledge fact IDs<textarea v-model="fields.factIDs" rows="2"></textarea></label><label>Knowledge document IDs<textarea v-model="fields.documentIDs" rows="2"></textarea></label><label>Baseline assessment IDs<textarea v-model="fields.assessmentIDs" rows="2"></textarea></label></details>
  <label>Operational reason<textarea v-model="fields.reason" minlength="3" maxlength="500" rows="3" required></textarea></label>
</template>
