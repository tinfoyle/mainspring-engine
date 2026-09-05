<script setup lang="ts">
import { operationsDirectory, type OperationsDirectoryPage, type OperationsDirectoryRequest } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

const emit = defineEmits<{ lookup: [input: { kind: "user_id" | "account_id"; value: string; ticket: string; reason: string }] }>();
const kind = ref<"users" | "teams">("users");
const pageSize = ref(25);
const ticket = ref("DIRECTORY-REVIEW");
const reason = ref("Review users and teams for administration.");
const report = ref<OperationsDirectoryPage>();
const busy = ref(false);
const error = ref("");
let generation = 0;
const totalPages = computed(() => Math.max(1, Math.ceil((report.value?.total ?? 0) / (report.value?.page_size ?? pageSize.value))));
const start = computed(() => report.value?.total ? (report.value.page - 1) * report.value.page_size + 1 : 0);
const end = computed(() => report.value ? Math.min(report.value.page * report.value.page_size, report.value.total) : 0);
const hasRows = computed(() => !!(report.value?.users.length || report.value?.teams.length));
function date(value: string) { return new Date(value).toLocaleDateString(); }
function status(value: string) { return value === "pending_verification" ? "Email not verified" : value.charAt(0).toUpperCase() + value.slice(1).replaceAll("_", " "); }

async function load(page = 1) {
 const requestID = ++generation;
 busy.value = true; error.value = ""; report.value = undefined;
 const input: OperationsDirectoryRequest = { kind: kind.value, page, page_size: pageSize.value, ticket: ticket.value.trim(), reason: reason.value.trim() };
 try {
  const result = await operationsDirectory(input);
  if (requestID !== generation) return;
  if (result.page > Math.max(1, Math.ceil(result.total / result.page_size))) {
   await load(Math.max(1, Math.ceil(result.total / result.page_size))); return;
  }
  report.value = result;
 } catch (value) {
  if (requestID === generation) error.value = value instanceof Error ? value.message : "The list could not be loaded. Try again.";
 } finally { if (requestID === generation) busy.value = false; }
}
function switchList(value: "users" | "teams") { kind.value = value; void load(); }
function lookup(value: string) {
 emit("lookup", { kind: kind.value === "users" ? "user_id" : "account_id", value, ticket: ticket.value.trim(), reason: reason.value.trim() });
}
onMounted(() => { void load(); });
onBeforeUnmount(() => { generation++; });
</script>

<template>
  <section aria-labelledby="directory-title">
    <p class="eyebrow">Administration</p><h1 id="directory-title">Users &amp; teams</h1>
    <p class="lede">Browse registered users and teams, newest first. Open a record in customer lookup for more details.</p>
    <div class="report-tabs" role="group" aria-label="Directory lists">
      <button type="button" :aria-pressed="kind === 'users'" :disabled="busy" @click="switchList('users')">Users</button>
      <button type="button" :aria-pressed="kind === 'teams'" :disabled="busy" @click="switchList('teams')">Teams</button>
    </div>
    <form class="form-card directory-controls" @submit.prevent="load()">
      <div class="directory-toolbar"><label>Rows per page<select v-model.number="pageSize" :disabled="busy" @change="load()"><option :value="25">25</option><option :value="50">50</option><option :value="100">100</option></select></label><IoButton type="submit" kind="secondary" :disabled="busy">Refresh list</IoButton></div>
      <details><summary>Review details</summary><div class="form-row"><label>Review reference<input v-model="ticket" required minlength="3" maxlength="80" :disabled="busy" /></label><label>Reason for viewing<input v-model="reason" required minlength="8" maxlength="500" :disabled="busy" /></label></div><p class="report-meta">Each page you open is recorded under your staff account.</p></details>
    </form>
    <p v-if="busy" role="status" class="empty-state">Loading {{ kind }}…</p>
    <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
    <template v-if="report">
      <div class="section-heading"><h2>{{ kind === 'users' ? 'Users' : 'Teams' }} <span class="directory-total">{{ report.total.toLocaleString() }}</span></h2><p class="report-meta">{{ kind === 'users' ? 'Team counts include active memberships.' : 'Member counts include active memberships.' }}</p></div>
      <p v-if="!hasRows" class="empty-state">{{ report.total ? 'No records on this page. Return to the first page or refresh the list.' : `No ${kind} yet.` }}</p>
      <div v-else class="table-wrap" tabindex="0" :aria-label="`Scrollable ${kind} list`">
        <table v-if="kind === 'users'">
          <caption>Users {{ start.toLocaleString() }}–{{ end.toLocaleString() }} of {{ report.total.toLocaleString() }}</caption>
          <thead><tr><th scope="col">Name</th><th scope="col">Email</th><th scope="col">Status</th><th scope="col">Teams</th><th scope="col">Joined</th><th scope="col">Details</th></tr></thead>
          <tbody><tr v-for="user in report.users" :key="user.id"><td class="directory-name">{{ user.display_name }}</td><td class="directory-email">{{ user.email }}<small>{{ user.email_verified ? 'Verified' : 'Not verified' }}</small></td><td><span class="state-pill">{{ status(user.state) }}</span></td><td>{{ user.team_count }}</td><td>{{ date(user.created_at) }}</td><td><button class="directory-link" type="button" :aria-label="`View user ${user.display_name}`" @click="lookup(user.id)">View user</button></td></tr></tbody>
        </table>
        <table v-else>
          <caption>Teams {{ start.toLocaleString() }}–{{ end.toLocaleString() }} of {{ report.total.toLocaleString() }}</caption>
          <thead><tr><th scope="col">Team</th><th scope="col">Status</th><th scope="col">Type</th><th scope="col">Members</th><th scope="col">Created</th><th scope="col">Details</th></tr></thead>
          <tbody><tr v-for="team in report.teams" :key="team.id"><td class="directory-name">{{ team.display_name }}<small>{{ team.slug }}</small></td><td><span class="state-pill">{{ status(team.state) }}</span></td><td>{{ status(team.account_type) }}</td><td>{{ team.member_count }}</td><td>{{ date(team.created_at) }}</td><td><button class="directory-link" type="button" :aria-label="`View team ${team.display_name}`" @click="lookup(team.id)">View team</button></td></tr></tbody>
        </table>
      </div>
      <nav class="directory-pagination" aria-label="Directory pages">
        <IoButton kind="secondary" :disabled="busy || report.page <= 1" @click="load(1)">First</IoButton>
        <IoButton kind="secondary" :disabled="busy || report.page <= 1" @click="load(report.page - 1)">Previous</IoButton>
        <span role="status">Page {{ report.page.toLocaleString() }} of {{ totalPages.toLocaleString() }}</span>
        <IoButton kind="secondary" :disabled="busy || report.page >= totalPages" @click="load(report.page + 1)">Next</IoButton>
      </nav>
    </template>
  </section>
</template>

<style scoped>
.section-heading { flex-wrap: wrap; gap: .25rem 1rem; margin-top: 1rem; }
.section-heading h2 { display: flex; align-items: baseline; gap: .5rem; margin: 0; }
.directory-controls { margin-top: 1.5rem; }
.directory-toolbar { display: flex; flex-wrap: wrap; align-items: end; gap: 1rem; }
.directory-toolbar label { min-width: 9rem; }
.directory-controls summary { cursor: pointer; color: var(--io-ocean-800); }
.directory-controls details .form-row { margin-top: 1rem; }
.directory-name, .directory-email { min-width: 12rem; max-width: 22rem; white-space: normal; overflow-wrap: anywhere; }
small { display: block; margin-top: .3rem; color: var(--io-ink-soft); }
.directory-total { color: var(--io-ink-soft); font-size: 1rem; font-weight: 500; }
.directory-link { background: transparent; color: var(--io-ocean-800); border: 0; padding: .5rem; text-decoration: underline; cursor: pointer; font: inherit; }
.directory-pagination { display: flex; flex-wrap: wrap; align-items: center; gap: .75rem; margin-top: 1.5rem; }
.directory-pagination span { padding: .5rem; }
@media (max-width: 30rem) {
 .directory-pagination { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .5rem; }
 .directory-pagination span { grid-column: 1 / -1; grid-row: 1; text-align: center; }
 .directory-pagination :deep(button) { min-width: 0; padding: .65rem .3rem; }
}
</style>
