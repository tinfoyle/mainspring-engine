<script setup lang="ts">
import { operationsTrafficReport, type OperationsTrafficReport } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref } from "vue";

const hours = ref(24);
const ticket = ref("TRAFFIC-REVIEW");
const reason = ref("Review site traffic and service health.");
const report = ref<OperationsTrafficReport>();
const busy = ref(false);
const error = ref("");
const tab = ref("summary");
const ipFilter = ref("");
const ips = computed(() => report.value?.ips.filter(row => row.ip.includes(ipFilter.value.trim())) ?? []);
const logs = computed(() => report.value?.logs.filter(row => row.ip.includes(ipFilter.value.trim())) ?? []);
const maximum = computed(() => Math.max(1, ...(report.value?.days.map(row => row.requests) ?? [])));
function date(value?: string | null) { return value ? new Date(value).toLocaleString() : "No requests recorded yet"; }

async function load() {
  busy.value = true; error.value = ""; report.value = undefined;
  try {
    const to = new Date();
    report.value = await operationsTrafficReport({ from: new Date(to.getTime() - hours.value * 3_600_000).toISOString(), to: to.toISOString(), ticket: ticket.value.trim(), reason: reason.value.trim() });
  } catch (value) { error.value = value instanceof Error ? value.message : "Traffic could not be loaded. Try again."; }
  finally { busy.value = false; }
}

function downloadIPs() {
  if (!report.value) return;
  const csv = ["IP,Requests,First seen (UTC),Last seen (UTC)", ...ips.value.map(row => [row.ip, row.requests, row.first_seen, row.last_seen].join(","))].join("\r\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" }));
  const link = document.createElement("a"); link.href = url; link.download = `spyglass-traffic-ips-${report.value.generated_at.slice(0, 10)}.csv`; link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
</script>

<template>
  <section aria-labelledby="traffic-title">
    <p class="eyebrow">Site traffic</p><h1 id="traffic-title">Traffic &amp; logs</h1>
    <p class="lede">Requests to the public website, app and APIs. This includes bots, files and service checks. A unique IP is a network address, not necessarily one person.</p>
    <form class="form-card" @submit.prevent="load">
      <div class="form-row"><label>Period<select v-model.number="hours"><option :value="1">Last hour</option><option :value="24">Last 24 hours</option><option :value="168">Last 7 days</option></select></label><label>Review reference<input v-model="ticket" required minlength="3" maxlength="80" /></label></div>
      <label>Reason for viewing<input v-model="reason" required minlength="8" maxlength="500" /></label>
      <IoButton type="submit" :disabled="busy">{{ busy ? "Reading logs…" : "Load traffic" }}</IoButton>
    </form>
    <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
    <template v-if="report">
      <p class="report-meta">Updated {{ date(report.generated_at) }}. Retained requests: {{ date(report.available_from) }} to {{ date(report.available_to) }}.</p>
      <p v-if="report.truncated || report.invalid_records" class="notice notice--warning" role="status">This report is incomplete. A log file is missing, a scan limit was reached, or records could not be read ({{ report.invalid_records }}). Counts may be lower than actual traffic. Check collection before relying on these totals.</p>
      <div class="metric-grid">
        <article><span>Requests</span><strong>{{ report.requests.toLocaleString() }}</strong></article>
        <article><span>Unique IPs</span><strong>{{ report.unique_ips.toLocaleString() }}</strong></article>
        <article><span>Server errors (5xx)</span><strong>{{ report.server_errors.toLocaleString() }}</strong></article>
        <article><span>Log files read</span><strong>{{ report.files_read }}</strong></article>
      </div>
      <div class="report-tabs" role="group" aria-label="Traffic views"><button v-for="view in [{ id: 'summary', label: 'Summary' }, { id: 'ips', label: 'Unique IPs' }, { id: 'logs', label: 'Request logs' }]" :key="view.id" type="button" :aria-pressed="tab === view.id" @click="tab = view.id">{{ view.label }}</button></div>
      <div v-if="tab === 'summary'" class="report-grid">
        <section class="report-card"><h2>Requests by day</h2><p v-if="!report.days.length">No requests in this period.</p><ol v-else class="traffic-bars"><li v-for="day in report.days" :key="day.label"><div><span>{{ day.label }} UTC</span><strong>{{ day.requests.toLocaleString() }}</strong></div><div class="traffic-bar" aria-hidden="true"><span :style="{ width: `${day.requests / maximum * 100}%` }" /></div></li></ol></section>
        <section class="report-card"><h2>Sites</h2><ul class="count-list"><li v-for="host in report.hosts" :key="host.label"><span>{{ host.label }}</span><strong>{{ host.requests.toLocaleString() }}</strong></li></ul><h2>Response codes</h2><ul class="count-list"><li v-for="status in report.statuses" :key="status.label"><span>{{ status.label }}</span><strong>{{ status.requests.toLocaleString() }}</strong></li></ul></section>
      </div>
      <template v-else>
        <div class="report-filter"><label>Filter by IP<input v-model="ipFilter" type="search" autocomplete="off" placeholder="Enter all or part of an IP" /></label><IoButton v-if="tab === 'ips'" kind="secondary" @click="downloadIPs">Download displayed IPs</IoButton></div>
        <p v-if="report.ip_list_truncated && tab === 'ips'" class="notice notice--warning">Showing the 1,000 busiest IPs. The summary includes all IPs counted in this scan.</p>
        <div v-if="tab === 'ips'" class="table-wrap" tabindex="0" aria-label="Scrollable IP addresses"><table><caption>{{ ips.length }} IP addresses displayed</caption><thead><tr><th scope="col">IP address</th><th scope="col">Requests</th><th scope="col">First seen</th><th scope="col">Last seen</th></tr></thead><tbody><tr v-for="row in ips" :key="row.ip"><td>{{ row.ip }}</td><td>{{ row.requests }}</td><td>{{ date(row.first_seen) }}</td><td>{{ date(row.last_seen) }}</td></tr></tbody></table></div>
        <div v-else class="table-wrap" tabindex="0" aria-label="Scrollable request logs"><table><caption>Most recent requests, up to 200. User agent identifies the browser or bot as reported by the visitor.</caption><thead><tr><th scope="col">Time</th><th scope="col">IP</th><th scope="col">Site</th><th scope="col">Method</th><th scope="col">Status</th><th scope="col">Time (ms)</th><th scope="col">User agent</th></tr></thead><tbody><tr v-for="(row, index) in logs" :key="index"><td>{{ date(row.time) }}</td><td>{{ row.ip }}</td><td>{{ row.host }}</td><td>{{ row.method }}</td><td>{{ row.status }}</td><td>{{ row.duration_ms }}</td><td class="user-agent"><template v-if="row.user_agent"><span>{{ row.user_agent.length > 120 ? row.user_agent.slice(0, 120) + "…" : row.user_agent }}</span><details v-if="row.user_agent.length > 120"><summary>Show full user agent</summary><p>{{ row.user_agent }}</p></details></template><span v-else>Not recorded</span></td></tr></tbody></table></div>
      </template>
    </template>
    <p v-else-if="!busy && !error" class="empty-state">Choose a period and load traffic. Reports use retained logs; visits before logging was enabled cannot be recovered.</p>
  </section>
</template>

<style scoped>
.user-agent { min-width: 16rem; max-width: 28rem; white-space: normal; overflow-wrap: anywhere; vertical-align: top; }
.user-agent summary { margin-top: .5rem; cursor: pointer; color: var(--io-ocean-800); }
.user-agent p { margin-bottom: 0; }
</style>
