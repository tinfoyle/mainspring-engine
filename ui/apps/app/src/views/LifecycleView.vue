<script setup lang="ts">
import { APIProblem, cancelAccountClosure, listAccountClosures, requestAccountClosure, type AccountClosure } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref } from "vue";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

type Action = "close" | "restore";
const session = useSessionStore(); const closures = ref<ReadonlyArray<AccountClosure>>([]); const loading = ref(true); const saving = ref(false);
const error = ref(""); const announcement = ref(""); const navigationNotice = ref(""); const securityRequired = ref(false); const actionOpen = ref(false); const action = ref<Action>("close");
const target = ref<AccountClosure>(); const reason = ref(""); const confirmation = ref("");
const canClose = computed(() => session.selected?.role === "owner" && !closures.value.some((item) => item.account_id === session.selectedID && ["cooling_off", "processing", "blocked"].includes(item.state)));
useSafeNavigation({
  dirty: actionOpen,
  pending: saving,
  message: "Leave Account lifecycle? Your open closure or restoration command will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This lifecycle operation is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your lifecycle command remains open.";
  }
});
function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Not scheduled"; }
function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function handle(cause: unknown): void { securityRequired.value = cause instanceof APIProblem && cause.status === 403; error.value = cause instanceof APIProblem ? cause.message : "Account lifecycle is unavailable right now."; }
async function load(): Promise<void> { loading.value = true; error.value = ""; securityRequired.value = false; try { closures.value = (await listAccountClosures()).account_closures; } catch (cause) { handle(cause); } finally { loading.value = false; } }
function begin(value: Action, item?: AccountClosure): void { action.value = value; target.value = item; reason.value = value === "restore" ? "Owner canceled Account closure" : ""; confirmation.value = ""; actionOpen.value = true; error.value = ""; securityRequired.value = false; }
function phrase(): string { return action.value === "close" ? "CLOSE" : "RESTORE"; }
async function submit(): Promise<void> {
  if (saving.value || confirmation.value !== phrase()) return; saving.value = true; navigationNotice.value = "";
  try {
    if (action.value === "close") { const selected = session.selected; if (!selected) return; await requestAccountClosure(selected.account_id, selected.account_version, reason.value.trim()); }
    else if (target.value) await cancelAccountClosure(target.value, reason.value.trim());
    const completed = action.value; actionOpen.value = false; announcement.value = completed === "close" ? "Account closure requested. Access is now frozen during cooling-off." : "Account restored.";
    await session.load(); await load();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 409) { actionOpen.value = false; await session.load(); await load(); error.value = "This Account changed. Review its current lifecycle state before trying again."; }
    else handle(cause);
  } finally { saving.value = false; }
}
void load();
</script>

<template>
  <section class="page lifecycle-page">
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice && !actionOpen" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading"><p class="eyebrow">Account lifecycle</p><h1>Deliberate and recoverable</h1><p>This identity-wide recovery surface remains available after Account access freezes. Closure is not immediate erasure and does not bypass governed retention.</p></header>
    <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }} <a v-if="securityRequired" href="/app/security?return_to=%2Fapp%2Faccount-closures">Confirm in Security</a></p>
    <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner identity first</h2><p>Finish two-factor authentication before using lifecycle authority.</p><a href="/app/security?return_to=%2Fapp%2Faccount-closures">Continue security setup</a></section>
    <section v-if="canClose && session.selected" class="lifecycle-request"><div><p class="eyebrow">Selected Account</p><h2>Close {{ session.selected.display_name }}</h2><p>Closure immediately freezes access for every member and begins a seven-day cooling-off period. Active subscriptions or checkout sessions must be resolved first.</p></div><IoButton @click="begin('close')">Request closure</IoButton></section>
    <section class="lifecycle-history">
      <header><div><p class="eyebrow">Global recovery</p><h2>Closure history</h2></div><span>{{ closures.length }} records</span></header>
      <div v-if="loading" class="queue-state" role="status">Loading lifecycle history…</div><div v-else-if="closures.length === 0" class="queue-state"><h3>No closure history</h3><p>Accounts you request to close will remain visible here during their governed lifecycle.</p></div>
      <ol v-else class="lifecycle-list"><li v-for="item in closures" :key="item.request_id"><div><span class="state-badge">{{ label(item.state) }}</span><h3>{{ item.account_name }}</h3><p>{{ item.reason }}</p><dl><div><dt>Requested</dt><dd>{{ date(item.requested_at) }}</dd></div><div><dt>Executes after</dt><dd>{{ date(item.execute_after) }}</dd></div><div v-if="item.delete_after"><dt>Retention deadline</dt><dd>{{ date(item.delete_after) }}</dd></div><div v-if="item.blocker_code"><dt>Blocked</dt><dd>{{ label(item.blocker_code) }}</dd></div><div><dt>Account version</dt><dd>{{ item.account_version }}</dd></div></dl></div><IoButton v-if="['cooling_off', 'blocked', 'processing'].includes(item.state)" kind="secondary" @click="begin('restore', item)">Restore Account</IoButton></li></ol>
    </section>
    <div v-if="actionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="lifecycle-action-title" @submit.prevent="submit"><h2 id="lifecycle-action-title">{{ action === "close" ? "Request Account closure?" : `Restore ${target?.account_name ?? "Account"}?` }}</h2><p v-if="action === 'close'">Access freezes immediately. Logical closure retains governed records until the separate retention deadline; it does not synchronously erase data.</p><label>Operational reason<textarea v-model="reason" minlength="3" maxlength="300" rows="4" required></textarea></label><label>Type {{ phrase() }} to confirm<input v-model="confirmation" autocomplete="off" :pattern="phrase()" required></label><p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="actionOpen = false">Back</IoButton><IoButton type="submit" :disabled="saving || confirmation !== phrase()">{{ saving ? "Saving…" : "Confirm" }}</IoButton></div></form></div>
  </section>
</template>
