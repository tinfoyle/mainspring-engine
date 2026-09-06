<script setup lang="ts">
import {
  APIProblem,
  attentionItemID,
  emitAnalytics,
  getPrivacyConsent,
  listAttentionQueue,
  type AttentionKind,
  type AttentionQueueItem
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { RouterLink } from "vue-router";
import { useSessionStore } from "../stores/session";

const session = useSessionStore();
const items = ref<ReadonlyArray<AttentionQueueItem>>([]);
const loading = ref(false);
const error = ref("");
const announcement = ref("");
const online = ref(typeof navigator === "undefined" ? true : navigator.onLine);
const filter = ref<"all" | AttentionKind>("all");
let requestSequence = 0;
let refreshTimer: number | undefined;
const instrumentedAccounts = new Set<string>();

const visibleItems = computed(() => filter.value === "all" ? items.value : items.value.filter((item) => item.kind === filter.value));
const counts = computed(() => ({
  all: items.value.length,
  information: items.value.filter((item) => item.kind === "information").length,
  review: items.value.filter((item) => item.kind === "review").length,
  approval: items.value.filter((item) => item.kind === "approval").length,
  action: items.value.filter((item) => item.kind === "action").length
}));

function label(value: string): string {
  return ({ information: "Information", review: "Work review", approval: "Approval", action: "Recovery" } as Record<string, string>)[value]
    ?? value.replaceAll("_", " ");
}

function title(item: AttentionQueueItem): string {
  if (item.kind === "information" || item.kind === "review") return item.question;
  return item.capability === "schedules.create" ? "Approve daily report" : item.capability === "marketing.release.activate" ? "Approve marketing campaign" : label(item.capability.replaceAll(".", " "));
}

function context(item: AttentionQueueItem): string {
  if (item.kind === "information") return "An agent needs an answer to continue.";
  if (item.kind === "review") return `Review Work version ${item.work_version}.`;
  if (item.kind === "approval") return "Review the proposed action and choose whether to approve it.";
  return `Attempt ${item.attempt_count} needs an evidence-based outcome.`;
}

function detailRoute(item: AttentionQueueItem): string {
  return `/app/your-turn/${item.kind}/${encodeURIComponent(attentionItemID(item))}`;
}

function relativeTime(value: string): string {
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return "Recently";
  const seconds = Math.round((timestamp - Date.now()) / 1000);
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  if (Math.abs(seconds) < 60) return formatter.format(seconds, "second");
  const minutes = Math.round(seconds / 60);
  if (Math.abs(minutes) < 60) return formatter.format(minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 24) return formatter.format(hours, "hour");
  return formatter.format(Math.round(hours / 24), "day");
}

async function refresh(announce = false): Promise<void> {
  const accountID = session.selectedID;
  const userID = session.userID;
  if (!accountID || !userID) {
    items.value = [];
    return;
  }
  const sequence = ++requestSequence;
  loading.value = true;
  error.value = "";
  try {
    const result = await listAttentionQueue(accountID, userID, session.attentionAccess);
    if (sequence !== requestSequence) return;
    items.value = result;
    if (announce) announcement.value = `Your Turn refreshed. ${result.length} open ${result.length === 1 ? "item" : "items"}.`;
    if (!instrumentedAccounts.has(accountID)) {
      instrumentedAccounts.add(accountID);
      try {
        const consent = await getPrivacyConsent();
        await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, {
          name: "your_turn_opened", fields: { queue_state: result.length === 0 ? "empty" : "open" }
        });
      } catch {
        // Product analytics never gates the governed queue.
      }
    }
  } catch (cause) {
    if (sequence !== requestSequence) return;
    error.value = cause instanceof APIProblem ? cause.message : "Your Turn is unavailable right now.";
  } finally {
    if (sequence === requestSequence) loading.value = false;
  }
}

function handleOnline(): void {
  online.value = true;
  void refresh(true);
}

function handleOffline(): void {
  online.value = false;
}

onMounted(() => {
  window.addEventListener("online", handleOnline);
  window.addEventListener("offline", handleOffline);
  refreshTimer = window.setInterval(() => {
    if (document.visibilityState === "visible" && navigator.onLine) void refresh();
  }, 30_000);
});

onBeforeUnmount(() => {
  window.removeEventListener("online", handleOnline);
  window.removeEventListener("offline", handleOffline);
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer);
});

watch(() => [session.selectedID, session.userID, session.attentionAccess.work, session.attentionAccess.approvals], () => void refresh(), { immediate: true });
</script>

<template>
  <section class="page your-turn">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <header class="page-heading page-heading--action">
      <div>
        <h1>Your Turn</h1>
        <p>Answer questions, review work, and approve actions that need your decision.</p>
      </div>
      <IoButton kind="secondary" :disabled="loading || !session.selectedID" @click="refresh(true)">{{ loading ? "Refreshing…" : "Refresh" }}</IoButton>
    </header>

    <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning">
      <h2>Secure this owner Account first</h2>
      <p>Finish two-factor authentication before Spyglass enables owner authority.</p>
      <a href="/app/security">Continue security setup</a>
    </section>

    <section v-else-if="!session.attentionAccess.work && !session.attentionAccess.approvals" class="queue-state">
      <h2>Your Turn is not enabled for this Account</h2>
      <p>The Work or Agents package makes Account-scoped questions and decisions available here.</p>
      <a href="/app/billing">Review Account plans</a>
    </section>

    <template v-else>
      <p v-if="!online" class="queue-inline-status" role="status">You are offline. Spyglass will refresh this queue when the connection returns.</p>
      <div class="queue-toolbar" role="group" aria-label="Filter Your Turn queue">
        <button v-for="choice in ([['all', 'All'], ['information', 'Information'], ['review', 'Reviews'], ['approval', 'Approvals'], ['action', 'Recovery']] as const)" :key="choice[0]" type="button" class="filter-chip" :class="{ 'filter-chip--active': filter === choice[0] }" :aria-pressed="filter === choice[0]" @click="filter = choice[0]">
          {{ choice[1] }} <strong>{{ counts[choice[0]] }}</strong>
        </button>
      </div>

      <div v-if="loading && items.length === 0" class="attention-list attention-list--loading" role="status">
        <span class="sr-only">Loading Your Turn…</span>
        <div v-for="index in 3" :key="index" class="attention-card attention-card--skeleton" aria-hidden="true" />
      </div>
      <section v-else-if="error && items.length === 0" class="queue-state queue-state--error" role="alert">
        <h2>That did not load cleanly</h2><p>{{ error }}</p><IoButton kind="secondary" @click="refresh()">Try again</IoButton>
      </section>
      <section v-else-if="visibleItems.length === 0" class="queue-state" role="status">
        <h2>Nothing needs you in this view</h2><p>Questions and actions that need your decision will appear here.</p>
      </section>
      <template v-else>
        <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }} Showing the last loaded queue.</p>
        <ol class="attention-list" aria-label="Items needing your attention">
          <li v-for="item in visibleItems" :key="`${item.kind}:${attentionItemID(item)}`">
            <article class="attention-card" :class="{ 'attention-card--urgent': item.kind === 'approval' || item.kind === 'action' }">
              <div class="card-meta"><span>{{ label(item.kind) }}</span><time :datetime="item.updated_at">{{ relativeTime(item.updated_at) }}</time></div>
              <h2>{{ title(item) }}</h2>
              <p>{{ context(item) }}</p>
              <footer>
                <span class="account-dot">{{ session.selected?.display_name }}</span>
                <RouterLink class="card-action" :to="detailRoute(item)">Review <span class="sr-only">{{ title(item) }}</span></RouterLink>
              </footer>
            </article>
          </li>
        </ol>
      </template>
    </template>
  </section>
</template>
