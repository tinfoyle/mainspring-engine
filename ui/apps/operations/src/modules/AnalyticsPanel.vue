<script setup lang="ts">
import { operationsAnalyticsReport, type OperationsAnalyticsReport } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref } from "vue";
const report = ref<OperationsAnalyticsReport>();
const days = ref(7); const dimension = ref("none"); const minimum = ref(5);
const ticket = ref("ANALYTICS-REVIEW"); const reason = ref("Review marketing and onboarding trends.");
const busy = ref(false); const error = ref("");
const events = computed(() => report.value?.rows.reduce((total, row) => total + row.event_count, 0) ?? 0);
const eventTypes = computed(() => new Set(report.value?.rows.map(row => row.event_name)).size);
function label(value: string) { return value.replaceAll("_", " "); }
async function load() {
  busy.value = true; error.value = ""; report.value = undefined;
  try {
    const to = new Date();
    report.value = await operationsAnalyticsReport({ from: new Date(to.getTime() - days.value * 86_400_000).toISOString(), to: to.toISOString(), bucket: "day", dimension: dimension.value, minimum_cohort: minimum.value, ticket: ticket.value.trim(), reason: reason.value.trim() });
  } catch (value) { error.value = value instanceof Error ? value.message : "Analytics could not be loaded. Try again."; }
  finally { busy.value = false; }
}
</script>

<template>
  <section aria-labelledby="analytics-title">
    <p class="eyebrow">Marketing and onboarding</p><h1 id="analytics-title">Product analytics</h1>
    <p class="lede">See page visits, signup steps and in-app activity from browsers that allowed analytics. Use Traffic &amp; logs for all HTTP requests, including visitors who declined analytics.</p>
    <form class="form-card" @submit.prevent="load">
      <div class="form-row"><label>Period<select v-model.number="days"><option :value="7">Last 7 days</option><option :value="30">Last 30 days</option><option :value="90">Last 90 days</option></select></label><label>Group by<select v-model="dimension"><option value="none">Event only</option><option value="route_name">Page</option><option value="device_class">Device</option><option value="cta_code">Button or link</option><option value="offer_code">Offer</option><option value="campaign_code">Campaign</option><option value="entry_point">Entry point</option><option value="result">Result</option></select></label><label>Minimum browsers per group<input v-model.number="minimum" type="number" min="5" max="100" required /></label></div>
      <div class="form-row"><label>Review reference<input v-model="ticket" required minlength="3" maxlength="80" /></label><label>Reason for viewing<input v-model="reason" required minlength="8" maxlength="500" /></label></div>
      <IoButton type="submit" :disabled="busy">{{ busy ? "Loading…" : "Run report" }}</IoButton>
    </form>
    <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
    <template v-if="report">
      <p v-if="report.rows.length === 0" class="empty-state" role="status">No groups meet these settings. Each daily event group needs at least {{ report.minimum_cohort }} browsers that allowed analytics. A quiet Stage site may have traffic without enough consented activity to show here. Try a longer period or choose Event only. This is not a count of zero visitors.</p>
      <template v-else>
        <div class="metric-grid"><article><span>Reported events</span><strong>{{ events.toLocaleString() }}</strong></article><article><span>Event types</span><strong>{{ eventTypes }}</strong></article></div>
        <div class="table-wrap" tabindex="0" aria-label="Scrollable analytics results"><table><caption>{{ report.rows.length }} groups · groups with fewer than {{ report.minimum_cohort }} browsers are hidden</caption><thead><tr><th scope="col">Day (UTC)</th><th scope="col">Event</th><th scope="col">Source</th><th scope="col">Group</th><th scope="col">Events</th><th scope="col">Browsers</th></tr></thead><tbody><tr v-for="row in report.rows" :key="`${row.bucket_start}:${row.event_name}:${row.surface}:${row.dimension_value}`"><td>{{ row.bucket_start.slice(0, 10) }}</td><td>{{ label(row.event_name) }}</td><td>{{ row.surface === 'conversion' ? 'Signup handoff' : row.surface === 'public' ? 'Website' : 'App' }}</td><td>{{ label(row.dimension_value) || 'All' }}</td><td>{{ row.event_count }}</td><td>{{ row.unique_subjects }}</td></tr></tbody></table></div>
      </template>
      <p class="report-meta">Browser counts are distinct within each row. Do not add them together to estimate people or conversion rates. Small groups and visitors who declined analytics are excluded.</p>
    </template>
    <p v-else-if="!busy && !error" class="empty-state">Choose a period and run the report. Analytics follows the visitor’s saved privacy choice.</p>
  </section>
</template>
