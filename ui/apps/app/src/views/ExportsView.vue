<script setup lang="ts">
import { APIProblem, cancelAccountExport, createAccountExport, createExportDownloadCapability, downloadAccountExport, listAccountExports, type AccountExport } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { useSessionStore } from "../stores/session";

type Action = "create" | "cancel";
const session = useSessionStore(); const exports = ref<ReadonlyArray<AccountExport>>([]); const loading = ref(false); const saving = ref(false);
const error = ref(""); const announcement = ref(""); const securityRequired = ref(false); const actionOpen = ref(false); const action = ref<Action>("create");
const target = ref<AccountExport>(); const confirmation = ref(""); let sequence = 0;
const owner = computed(() => session.selected?.role === "owner");
function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Not available"; }
function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function message(cause: unknown, fallback: string): string { return cause instanceof APIProblem ? cause.message : fallback; }
function handleError(cause: unknown): void {
  securityRequired.value = cause instanceof APIProblem && cause.status === 403;
  error.value = message(cause, "The export operation could not be completed.");
}
async function load(): Promise<void> {
  const accountID = session.selectedID; const current = ++sequence; exports.value = []; error.value = ""; securityRequired.value = false;
  if (!accountID || !owner.value) return; loading.value = true;
  try { const result = await listAccountExports(accountID); if (current === sequence) exports.value = result.exports; }
  catch (cause) { if (current === sequence) handleError(cause); }
  finally { if (current === sequence) loading.value = false; }
}
function begin(value: Action, item?: AccountExport): void { action.value = value; target.value = item; confirmation.value = ""; actionOpen.value = true; error.value = ""; securityRequired.value = false; }
function phrase(): string { return action.value === "create" ? "EXPORT" : "CANCEL"; }
async function submit(): Promise<void> {
  const accountID = session.selectedID; if (!accountID || saving.value || confirmation.value !== phrase()) return; saving.value = true;
  try {
    if (action.value === "create") await createAccountExport(accountID);
    else if (target.value) await cancelAccountExport(accountID, target.value);
    actionOpen.value = false; announcement.value = action.value === "create" ? "Account export requested." : "Account export canceled."; await load();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 409) { actionOpen.value = false; await load(); error.value = "This export changed. Review its current state before trying again."; }
    else handleError(cause);
  } finally { saving.value = false; }
}
async function download(item: AccountExport): Promise<void> {
  const accountID = session.selectedID; if (!accountID || saving.value) return; saving.value = true; error.value = ""; securityRequired.value = false;
  try {
    const capability = await createExportDownloadCapability(accountID, item.id); const blob = await downloadAccountExport(item.id, capability.token);
    const url = URL.createObjectURL(blob); const anchor = document.createElement("a"); anchor.href = url; anchor.download = `spyglass-account-export-${item.id}.zip`; document.body.append(anchor); anchor.click(); anchor.remove(); window.setTimeout(() => URL.revokeObjectURL(url), 0);
    announcement.value = "Account export download started.";
  } catch (cause) { handleError(cause); }
  finally { saving.value = false; }
}
watch(() => [session.selectedID, session.selected?.role], () => void load(), { immediate: true });
</script>

<template>
  <section class="page exports-page">
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <header class="page-heading"><p class="eyebrow">Account portability</p><h1>Take your Account with you</h1><p>Exports combine the governed global Account record with current cell data into one immutable ZIP retained for seven days.</p></header>
    <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Exports always belong to one Account.</p></section>
    <section v-else-if="!owner" class="queue-state"><h2>Owner access required</h2><p>Only an Account owner can request or download a complete Account export.</p></section>
    <template v-else>
      <section v-if="session.selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner identity first</h2><p>Add a passkey and save recovery codes before exporting Account data.</p><a href="/app/security?return_to=%2Fapp%2Faccount-exports">Continue security setup</a></section>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }} <a v-if="securityRequired" href="/app/security?return_to=%2Fapp%2Faccount-exports">Confirm in Security</a></p>
      <section class="export-request"><div><p class="eyebrow">New snapshot</p><h2>Request Account export</h2><p>Requires a passkey confirmation from the last ten minutes. Only one build may be active for an Account.</p></div><IoButton :disabled="saving" @click="begin('create')">Request export</IoButton></section>
      <section class="export-history">
        <header><div><p class="eyebrow">Export history</p><h2>Requests and artifacts</h2></div><span>{{ exports.length }} records</span></header>
        <div v-if="loading" class="queue-state" role="status">Loading export history…</div><div v-else-if="exports.length === 0" class="queue-state"><h3>No export history</h3><p>Requested Account snapshots will appear here.</p></div>
        <ol v-else class="export-list"><li v-for="item in exports" :key="item.id"><div><span class="state-badge">{{ label(item.state) }}</span><h3>Requested {{ date(item.requested_at) }}</h3><small class="digest">{{ item.id }}</small><dl><div><dt>Expires</dt><dd>{{ date(item.expires_at) }}</dd></div><div v-if="item.artifact_bytes"><dt>Artifact</dt><dd>{{ item.artifact_bytes.toLocaleString() }} bytes</dd></div><div v-if="item.error_code"><dt>Result</dt><dd>{{ label(item.error_code) }}</dd></div><div><dt>Version</dt><dd>{{ item.version }}</dd></div></dl></div><aside><IoButton v-if="item.state === 'available'" :disabled="saving" @click="download(item)">Download ZIP</IoButton><IoButton v-if="item.state === 'queued'" kind="secondary" :disabled="saving" @click="begin('cancel', item)">Cancel request</IoButton></aside></li></ol>
      </section>
    </template>
    <div v-if="actionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="export-action-title" @submit.prevent="submit"><h2 id="export-action-title">{{ action === "create" ? "Request an Account export?" : "Cancel this export request?" }}</h2><p>{{ action === "create" ? "The snapshot may contain sensitive Account data and is retained for seven days." : "A canceled queued request cannot continue building." }}</p><label>Type {{ phrase() }} to confirm<input v-model="confirmation" autocomplete="off" :pattern="phrase()" required></label><div class="modal-actions"><IoButton type="button" kind="secondary" @click="actionOpen = false">Back</IoButton><IoButton type="submit" :disabled="saving || confirmation !== phrase()">{{ saving ? "Saving…" : "Confirm" }}</IoButton></div></form></div>
  </section>
</template>
